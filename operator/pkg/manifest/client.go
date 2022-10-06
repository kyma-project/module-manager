package manifest

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/kyma-project/module-manager/operator/pkg/types"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"

	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type OperationType string

type HelmOperation OperationType

const (
	OperationCreate HelmOperation = "create"
	OperationDelete HelmOperation = "delete"
)

type HelmClient struct {
	kubeClient *kube.Client
	settings   *cli.EnvSettings
	restGetter *manifestRest.ManifestRESTClientGetter
	clientSet  *kubernetes.Clientset
	restConfig *rest.Config
	mapper     *restmapper.DeferredDiscoveryRESTMapper
}

//nolint:gochecknoglobals
var accessor = meta.NewAccessor()

func NewHelmClient(kubeClient *kube.Client, restGetter *manifestRest.ManifestRESTClientGetter,
	restConfig *rest.Config, settings *cli.EnvSettings,
) (*HelmClient, error) {
	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return &HelmClient{}, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create new discovery client %w", err)
	}

	discoveryMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	return &HelmClient{
		kubeClient: kubeClient,
		settings:   settings,
		restGetter: restGetter,
		clientSet:  clientSet,
		restConfig: restConfig,
		mapper:     discoveryMapper,
	}, nil
}

func (h *HelmClient) getGenericConfig(namespace string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.restGetter, namespace, "secrets",
		func(format string, v ...interface{}) {
			callDepth := 2
			format = fmt.Sprintf("[debug] %s\n", format)
			err := log.Output(callDepth, fmt.Sprintf(format, v...))
			if err != nil {
				log.Println(err.Error())
			}
		}); err != nil {
		return nil, err
	}
	return actionConfig, nil
}

func (h *HelmClient) NewInstallActionClient(namespace, releaseName string, configFlags map[string]interface{},
) (*action.Install, error) {
	actionConfig, err := h.getGenericConfig(namespace)
	if err != nil {
		return nil, err
	}
	actionClient := action.NewInstall(actionConfig)
	h.SetDefaultClientConfig(actionClient, releaseName)
	return actionClient, h.SetConfigFlags(configFlags, actionClient)
}

func (h *HelmClient) NewUninstallActionClient(namespace string) (*action.Uninstall, error) {
	actionConfig, err := h.getGenericConfig(namespace)
	if err != nil {
		return nil, err
	}
	return action.NewUninstall(actionConfig), nil
}

func (h *HelmClient) SetDefaultClientConfig(actionClient *action.Install, releaseName string) {
	actionClient.DryRun = false
	actionClient.Atomic = false

	// Wait until all Pods, Services, etc. of the Deployment, StatefulSet, or ReplicaSet are in a
	// ready state before marking the release as successful
	// It will wait for as long as timeout is set
	actionClient.Wait = true
	actionClient.WaitForJobs = false
	actionClient.Timeout = 5 * time.Minute

	actionClient.Replace = true     // Skip the name check
	actionClient.IncludeCRDs = true // include CRDs in the templated output

	// ClientOnly has no interaction with the API server
	// So unless mentioned no additional API Versions can be used as part of helm chart installation
	actionClient.ClientOnly = false

	actionClient.ReleaseName = releaseName
	actionClient.Namespace = v1.NamespaceDefault
	// this will prohibit resource conflict validation while uninstalling
	actionClient.IsUpgrade = true

	// default versioning if unspecified
	if actionClient.Version == "" && actionClient.Devel {
		actionClient.Version = ">0.0.0-0"
	}
}

func (h *HelmClient) SetConfigFlags(configFlags map[string]interface{}, actionClient *action.Install) error {
	clientValue := reflect.Indirect(reflect.ValueOf(actionClient))

	for flagKey, flagValue := range configFlags {
		value := clientValue.FieldByName(flagKey)
		if !value.IsValid() || !value.CanSet() {
			continue
		}

		validConversion := true

		//nolint:exhaustive
		switch value.Kind() {
		case reflect.Bool:
			var valueToBeSet bool
			valueToBeSet, validConversion = flagValue.(bool)
			if validConversion {
				value.SetBool(valueToBeSet)
			}
		case reflect.Int, reflect.Int64:
			var valueToBeSet int64
			valueToBeSet, validConversion = flagValue.(int64)
			if validConversion {
				value.SetInt(valueToBeSet)
			} else {
				var fallbackInt64 time.Duration
				fallbackInt64, validConversion = flagValue.(time.Duration)
				if validConversion {
					value.SetInt(int64(fallbackInt64))
				}
			}
		case reflect.String:
			var valueToBeSet string
			valueToBeSet, validConversion = flagValue.(string)
			if validConversion {
				value.SetString(valueToBeSet)
			}
		}

		if !validConversion {
			return fmt.Errorf("unsupported flag value %s:%v", flagKey, flagValue)
		}
	}
	return nil
}

func (h *HelmClient) DownloadChart(actionClient *action.Install, chartName string) (string, error) {
	return actionClient.ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *HelmClient) GetNsResource(actionClient *action.Install, operationType HelmOperation,
) (kube.ResourceList, error) {
	// set kubeclient namespace for override
	h.kubeClient.Namespace = actionClient.Namespace

	// validate namespace parameters
	// proceed only if not default namespace since it already exists
	if !actionClient.CreateNamespace || actionClient.Namespace == v1.NamespaceDefault {
		return nil, nil
	}

	ns := actionClient.Namespace
	nsBuf, err := util.GetNamespaceObjBytes(ns)
	if err != nil {
		return nil, err
	}
	return h.kubeClient.Build(bytes.NewBuffer(nsBuf), true)
}

func (h *HelmClient) CreateNamespace(namespace kube.ResourceList) error {
	if _, err := h.kubeClient.Create(namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (h *HelmClient) DeleteNamespace(namespace kube.ResourceList) error {
	if _, delErrors := h.kubeClient.Delete(namespace); len(delErrors) > 0 {
		var wrappedError error
		for _, err := range delErrors {
			wrappedError = fmt.Errorf("%w", err)
		}
		return wrappedError
	}
	return nil
}

func newRestClient(restConfig rest.Config, gv schema.GroupVersion) (resource.RESTClient, error) {
	restConfig.ContentConfig = resource.UnstructuredPlusDefaultContentConfig()
	restConfig.GroupVersion = &gv

	if len(gv.Group) == 0 {
		restConfig.APIPath = "/api"
	} else {
		restConfig.APIPath = "/apis"
	}

	return rest.RESTClientFor(&restConfig)
}

func (h *HelmClient) assignRestMapping(gvk schema.GroupVersionKind, info *resource.Info) error {
	restMapping, err := h.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		h.mapper.Reset()
		return err
	}
	info.Mapping = restMapping
	return nil
}

func (h *HelmClient) convertToInfo(unstructuredObj *unstructured.Unstructured) (*resource.Info, error) {
	info := &resource.Info{}
	gvk := unstructuredObj.GroupVersionKind()
	gv := gvk.GroupVersion()
	client, err := newRestClient(*h.restConfig, gv)
	if err != nil {
		return nil, err
	}
	info.Client = client
	if err = h.assignRestMapping(gvk, info); err != nil {
		return nil, err
	}

	info.Namespace = unstructuredObj.GetNamespace()
	info.Name = unstructuredObj.GetName()
	info.Object = unstructuredObj.DeepCopyObject()
	return info, nil
}

func (h *HelmClient) transformManifestResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	objects, err := util.ParseManifestStringToObjects(manifest)
	if err != nil {
		return nil, err
	}

	for _, transform := range transforms {
		if err = transform(ctx, object, objects); err != nil {
			return nil, err
		}
	}

	for _, unstructuredObject := range objects.Items {
		resourceInfo, err := h.convertToInfo(unstructuredObject)
		if err != nil {
			return nil, err
		}
		resourceList = append(resourceList, resourceInfo)
	}
	return resourceList, err
}

func (h *HelmClient) GetTargetResources(ctx context.Context, manifest string, targetNamespace string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	var err error

	if len(transforms) == 0 {
		resourceList, err = h.kubeClient.Build(bytes.NewBufferString(manifest), true)
	} else {
		resourceList, err = h.transformManifestResources(ctx, manifest, transforms, object)
	}

	if err != nil {
		return nil, err
	}

	// verify namespace override if not done by kubeclient
	if err = h.overrideNamespace(resourceList, targetNamespace); err != nil {
		return nil, err
	}
	return resourceList, nil
}

func (h *HelmClient) PerformUpdate(resourceLists ResourceLists, force bool,
) (*kube.Result, error) {
	return h.kubeClient.Update(resourceLists.Installed, resourceLists.Target, force)
}

func (h *HelmClient) PerformCreate(resourceLists ResourceLists) (*kube.Result, error) {
	return h.kubeClient.Create(resourceLists.Target)
}

func (h *HelmClient) CheckWaitForResources(targetResources kube.ResourceList, actionClient *action.Install,
	operation HelmOperation,
) error {
	if !actionClient.Wait || actionClient.Timeout == 0 {
		return nil
	}

	if operation == OperationDelete {
		return h.kubeClient.WaitForDelete(targetResources, actionClient.Timeout)
	}

	if actionClient.WaitForJobs {
		return h.kubeClient.WaitWithJobs(targetResources, actionClient.Timeout)
	}
	return h.kubeClient.Wait(targetResources, actionClient.Timeout)
}

func (h *HelmClient) CheckDesiredState(ctx context.Context, targetResources kube.ResourceList, operation HelmOperation,
) (bool, error) {
	if operation == OperationDelete {
		return h.checkResourcesDeleted(targetResources)
	}

	readyChecker := kube.NewReadyChecker(h.clientSet, func(format string, v ...interface{}) {},
		kube.PausedAsReady(true), kube.CheckJobs(true))
	return h.checkReady(ctx, targetResources, readyChecker)
}

func (h *HelmClient) checkResourcesDeleted(targetResources kube.ResourceList) (bool, error) {
	resourcesDeleted := true
	err := targetResources.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		err = info.Get()
		if err == nil || !apierrors.IsNotFound(err) {
			resourcesDeleted = false
			return err
		}
		return nil
	})
	return resourcesDeleted, err
}

func (h *HelmClient) setNamespaceIfNotPresent(targetNamespace string, resourceInfo *resource.Info,
	helper *resource.Helper, runtimeObject runtime.Object,
) error {
	// check if resource is scoped to namespaces
	if helper.NamespaceScoped && resourceInfo.Namespace == "" {
		// check existing namespace - continue only if not set
		if targetNamespace == "" {
			targetNamespace = v1.NamespaceDefault
		}

		// set namespace on request
		resourceInfo.Namespace = targetNamespace
		if _, err := meta.Accessor(runtimeObject); err != nil {
			return err
		}

		// set namespace on runtime object
		return accessor.SetNamespace(runtimeObject, targetNamespace)
	}
	return nil
}

func (h *HelmClient) overrideNamespace(resourceList kube.ResourceList, targetNamespace string) error {
	return resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		helper := resource.NewHelper(info.Client, info.Mapping)
		return h.setNamespaceIfNotPresent(targetNamespace, info, helper, info.Object)
	})
}

func (h *HelmClient) checkReady(ctx context.Context, resourceList kube.ResourceList,
	readyChecker kube.ReadyChecker,
) (bool, error) {
	resourcesReady := true
	err := resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		if ready, err := readyChecker.IsReady(ctx, info); !ready || err != nil {
			resourcesReady = ready
			return err
		}
		return nil
	})
	return resourcesReady, err
}
