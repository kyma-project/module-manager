package manifest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"

	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type HelmClient struct {
	kubeClient   *kube.Client
	settings     *cli.EnvSettings
	restGetter   *manifestRest.ManifestRESTClientGetter
	clientSet    *kubernetes.Clientset
	restConfig   *rest.Config
	mapper       *restmapper.DeferredDiscoveryRESTMapper
	actionClient *action.Install
	repoHandler  *RepoHandler
	logger       *logr.Logger
}

//nolint:gochecknoglobals
var accessor = meta.NewAccessor()

func NewHelmClient(restGetter *manifestRest.ManifestRESTClientGetter, restConfig *rest.Config,
	settings *cli.EnvSettings, releaseName string, flags types.ChartFlags, logger *logr.Logger,
) (*HelmClient, error) {
	discoveryClient, err := restGetter.ToDiscoveryClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create new discovery client %w", err)
	}

	// Use deferred discovery client here as GVs applicable to the client are inconsistent at this moment
	discoveryMapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)

	helmClient := &HelmClient{
		logger:      logger,
		repoHandler: NewRepoHandler(logger, settings),
		settings:    settings,
		restGetter:  restGetter,
		restConfig:  restConfig,
		mapper:      discoveryMapper,
	}

	helmClient.actionClient, helmClient.kubeClient, err = helmClient.newInstallActionClient(
		v1.NamespaceDefault, restGetter)
	if err != nil {
		return nil, err
	}

	helmClient.clientSet, err = helmClient.kubeClient.Factory.KubernetesClientSet()
	if err != nil {
		return nil, err
	}

	// set preliminary flag defaults for helm installation
	helmClient.setDefaultFlags(releaseName)

	// set custom flags for helm installation
	return helmClient, helmClient.setCustomFlags(flags)
}

func (h *HelmClient) newInstallActionClient(namespace string, restGetter *manifestRest.ManifestRESTClientGetter,
) (*action.Install, *kube.Client, error) {
	actionConfig, err := h.getGenericConfig(namespace, restGetter)
	if err != nil {
		return nil, nil, err
	}
	kubeClient, ok := actionConfig.KubeClient.(*kube.Client)
	if !ok {
		return nil, nil, fmt.Errorf("invalid kubeclient generation for helm installation")
	}
	return action.NewInstall(actionConfig), kubeClient, nil
}

func (h *HelmClient) getGenericConfig(namespace string, restGetter *manifestRest.ManifestRESTClientGetter,
) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(restGetter, namespace, "secrets",
		func(format string, v ...interface{}) {
			format = fmt.Sprintf("%s\n", format)
			h.logger.V(util.DebugLogLevel).Info(fmt.Sprintf(format, v...))
		}); err != nil {
		return nil, err
	}
	return actionConfig, nil
}

func (h *HelmClient) setDefaultFlags(releaseName string) {
	h.actionClient.DryRun = true
	h.actionClient.Atomic = false

	h.actionClient.WaitForJobs = false

	h.actionClient.Replace = true     // Skip the name check
	h.actionClient.IncludeCRDs = true // include CRDs in the templated output
	h.actionClient.UseReleaseName = false
	h.actionClient.ReleaseName = releaseName

	// ClientOnly has no interaction with the API server
	// So unless mentioned no additional API Versions can be used as part of helm chart installation
	h.actionClient.ClientOnly = false

	h.actionClient.Namespace = v1.NamespaceDefault
	// this will prohibit resource conflict validation while uninstalling
	h.actionClient.IsUpgrade = true

	// default versioning if unspecified
	if h.actionClient.Version == "" && h.actionClient.Devel {
		h.actionClient.Version = ">0.0.0-0"
	}
}

func (h *HelmClient) setCustomFlags(flags types.ChartFlags) error {
	clientValue := reflect.Indirect(reflect.ValueOf(h.actionClient))

	// TODO: as per requirements add more Kind types
	for flagKey, flagValue := range flags.ConfigFlags {
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

func (h *HelmClient) DownloadChart(repoName, url, chartName string) (string, error) {
	err := h.repoHandler.Add(repoName, url)
	if err != nil {
		return "", err
	}
	return h.actionClient.ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *HelmClient) RenderManifestFromChartPath(chartPath string, flags types.Flags) (string, error) {
	// if rendered manifest doesn't exist
	chartRequested, err := h.repoHandler.LoadChart(chartPath, h.actionClient)
	if err != nil {
		return "", err
	}

	// retrieve manifest
	release, err := h.actionClient.Run(chartRequested, flags)
	if err != nil {
		return "", err
	}

	return release.Manifest, nil
}

func (h *HelmClient) UpdateRepo(repoName, url string) error {
	return h.repoHandler.Add(repoName, url)
}

func (h *HelmClient) GetNsResource() (kube.ResourceList, error) {
	// set kubeclient namespace for override
	h.kubeClient.Namespace = h.actionClient.Namespace

	// validate namespace parameters
	// proceed only if not default namespace since it already exists
	if !h.actionClient.CreateNamespace || h.actionClient.Namespace == v1.NamespaceDefault {
		return nil, nil
	}

	ns := h.actionClient.Namespace
	nsBuf, err := util.GetNamespaceObjBytes(ns)
	if err != nil {
		return nil, err
	}
	return h.kubeClient.Build(bytes.NewBuffer(nsBuf), true)
}

func (h *HelmClient) createNamespace(namespace kube.ResourceList) error {
	if _, err := h.kubeClient.Create(namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (h *HelmClient) deleteNamespace(namespace kube.ResourceList) error {
	if _, delErrors := h.kubeClient.Delete(namespace); len(delErrors) > 0 {
		var wrappedError error
		for _, err := range delErrors {
			wrappedError = fmt.Errorf("%w", err)
		}
		return wrappedError
	}
	return nil
}

func newRestClient(restConfig *rest.Config, gv schema.GroupVersion) (resource.RESTClient, error) {
	restConfig.ContentConfig = resource.UnstructuredPlusDefaultContentConfig()
	restConfig.GroupVersion = &gv

	if len(gv.Group) == 0 {
		restConfig.APIPath = "/api"
	} else {
		restConfig.APIPath = "/apis"
	}

	return rest.RESTClientFor(restConfig)
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
	// manual invalidation of mem cache client to maintain the current state
	h.mapper.Reset()
	info := &resource.Info{}
	gvk := unstructuredObj.GroupVersionKind()
	gv := gvk.GroupVersion()
	client, err := newRestClient(h.restConfig, gv)
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

func (h *HelmClient) GetTargetResources(ctx context.Context, manifest string,
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
	if err = overrideNamespace(resourceList, h.actionClient.Namespace); err != nil {
		return nil, err
	}
	return resourceList, nil
}

func (h *HelmClient) PerformUpdate(resourceLists types.ResourceLists, force bool,
) (*kube.Result, error) {
	// create namespace resource first
	if len(resourceLists.Namespace) > 0 {
		if err := h.createNamespace(resourceLists.Namespace); err != nil {
			return nil, err
		}
	}
	return h.kubeClient.Update(resourceLists.Installed, resourceLists.Target, force)
}

func (h *HelmClient) PerformCreate(resourceLists types.ResourceLists) (*kube.Result, error) {
	// create namespace resource first
	if len(resourceLists.Namespace) > 0 {
		if err := h.createNamespace(resourceLists.Namespace); err != nil {
			return nil, err
		}
	}
	return h.kubeClient.Create(resourceLists.Target)
}

func (h *HelmClient) PerformDelete(resourceLists types.ResourceLists) (int, error) {
	count := 0
	if resourceLists.Installed != nil {
		response, delErrors := h.kubeClient.Delete(resourceLists.Installed)
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err := range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return 0, wrappedError
		}

		count = len(response.Deleted)
	}

	if len(resourceLists.Namespace) > 0 {
		count++
		if err := h.deleteNamespace(resourceLists.Namespace); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (h *HelmClient) CheckWaitForResources(ctx context.Context, targetResources kube.ResourceList,
	operation types.HelmOperation, verifyWithoutTimeout bool,
) (bool, error) {
	// verifyWithoutTimeout flag checks native resources are in their respective ready states
	// without a timeout defined
	if verifyWithoutTimeout {
		if operation == types.OperationDelete {
			return checkResourcesDeleted(targetResources)
		}

		readyChecker := kube.NewReadyChecker(h.clientSet, func(format string, v ...interface{}) {},
			kube.PausedAsReady(true), kube.CheckJobs(true))
		return checkReady(ctx, targetResources, readyChecker)
	}

	// if Wait or WaitForJobs is enabled, resources are verified to be in ready state with a timeout
	if !h.actionClient.Wait || h.actionClient.Timeout == 0 {
		// return here as ready, since waiting flags were not set
		return true, nil
	}

	if operation == types.OperationDelete {
		// WaitForDelete reports an error if resources are not deleted in the specified timeout
		return true, h.kubeClient.WaitForDelete(targetResources, h.actionClient.Timeout)
	}

	if h.actionClient.WaitForJobs {
		// WaitWithJobs reports an error if resources are not deleted in the specified timeout
		return true, h.kubeClient.WaitWithJobs(targetResources, h.actionClient.Timeout)
	}
	// Wait reports an error if resources are not deleted in the specified timeout
	return true, h.kubeClient.Wait(targetResources, h.actionClient.Timeout)
}

func (h *HelmClient) UpdateRepos(ctx context.Context) error {
	return h.repoHandler.Update(ctx)
}

func checkResourcesDeleted(targetResources kube.ResourceList) (bool, error) {
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

func setNamespaceIfNotPresent(targetNamespace string, resourceInfo *resource.Info,
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

func overrideNamespace(resourceList kube.ResourceList, targetNamespace string) error {
	return resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		helper := resource.NewHelper(info.Client, info.Mapping)
		return setNamespaceIfNotPresent(targetNamespace, info, helper, info.Object)
	})
}

func checkReady(ctx context.Context, resourceList kube.ResourceList,
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
