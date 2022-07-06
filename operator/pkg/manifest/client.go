package manifest

import (
	"bytes"
	"context"
	"fmt"
	manifestRest "github.com/kyma-project/manifest-operator/operator/pkg/rest"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/strvals"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"
	"reflect"
	"time"
)

type OperationType string

type HelmOperation OperationType

const (
	OperationCreate HelmOperation = "create"
	OperationDelete HelmOperation = "delete"
)

type HelmClient struct {
	kubeClient  *kube.Client
	settings    *cli.EnvSettings
	restGetter  *manifestRest.ManifestRESTClientGetter
	clientSet   *kubernetes.Clientset
	waitTimeout time.Duration
}

func NewHelmClient(kubeClient *kube.Client, restGetter *manifestRest.ManifestRESTClientGetter, clientSet *kubernetes.Clientset, settings *cli.EnvSettings) *HelmClient {
	return &HelmClient{
		kubeClient: kubeClient,
		settings:   settings,
		restGetter: restGetter,
		clientSet:  clientSet,
	}
}

func (h *HelmClient) getGenericConfig(namespace string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.restGetter, namespace, "secrets", func(format string, v ...interface{}) {
		fmt.Printf(format, v)
	}); err != nil {
		return nil, err
	}
	return actionConfig, nil
}

func (h *HelmClient) NewInstallActionClient(namespace, releaseName string, args map[string]string) (*action.Install, error) {
	actionConfig, err := h.getGenericConfig(namespace)
	if err != nil {
		return nil, err
	}
	actionClient := action.NewInstall(actionConfig)
	h.SetDefaultClientConfig(actionClient, releaseName)
	return actionClient, h.SetFlags(args, actionClient)
}

func (h *HelmClient) NewUninstallActionClient(namespace string) (*action.Uninstall, error) {
	actionConfig, err := h.getGenericConfig(namespace)
	if err != nil {
		return nil, err
	}
	return action.NewUninstall(actionConfig), nil
}

func (h *HelmClient) SetDefaultClientConfig(actionClient *action.Install, releaseName string) {
	actionClient.DryRun = true
	actionClient.Atomic = false
	actionClient.Wait = false
	actionClient.WaitForJobs = false
	actionClient.DryRun = true
	actionClient.Replace = true     // Skip the name check
	actionClient.IncludeCRDs = true //include CRDs in the templated output
	actionClient.ClientOnly = true
	actionClient.ReleaseName = releaseName
	actionClient.Namespace = v1.NamespaceDefault

	// default versioning if unspecified
	if actionClient.Version == "" && actionClient.Devel {
		actionClient.Version = ">0.0.0-0"
	}
}

func (h *HelmClient) SetFlags(args map[string]string, actionClient *action.Install) error {
	clientFlags := map[string]interface{}{}
	if err := strvals.ParseInto(args["flags"], clientFlags); err != nil {
		return err
	}
	clientValue := reflect.Indirect(reflect.ValueOf(actionClient))

	// TODO: as per requirements add more Kind types
	for flagKey, flagValue := range clientFlags {
		value := clientValue.FieldByName(flagKey)
		if !value.IsValid() || !value.CanSet() {
			continue
		}
		switch value.Kind() {
		case reflect.Bool:
			value.SetBool(flagValue.(bool))
		case reflect.Int64:
			value.SetInt(flagValue.(int64))
		case reflect.String:
			value.SetString(flagValue.(string))
		}
	}
	return nil
}

func (h *HelmClient) DownloadChart(actionClient *action.Install, chartName string) (string, error) {
	return actionClient.ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *HelmClient) HandleNamespace(actionClient *action.Install, operationType HelmOperation) error {
	if actionClient.CreateNamespace {
		// validate namespace parameters
		// proceed only if not default namespace since it already exists
		if actionClient.Namespace == v1.NamespaceDefault {
			return nil
		}
		ns := actionClient.Namespace
		buf, err := util.GetNamespaceObjBytes(ns)
		if err != nil {
			return err
		}
		resourceList, err := h.kubeClient.Build(bytes.NewBuffer(buf), true)
		if err != nil {
			return err
		}

		switch operationType {
		case OperationCreate:
			if _, err = h.kubeClient.Create(resourceList); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
		case OperationDelete:
			if _, delErrors := h.kubeClient.Delete(resourceList); len(delErrors) > 0 {
				var wrappedError error
				for _, err = range delErrors {
					wrappedError = fmt.Errorf("%w", err)
				}
				return wrappedError
			}
		default:
		}
	}
	// set kubeclient namespace for override
	h.kubeClient.Namespace = actionClient.Namespace
	return nil
}

func (h *HelmClient) GetTargetResources(manifest string, targetNamespace string) (kube.ResourceList, error) {
	resourceList, err := h.kubeClient.Build(bytes.NewBufferString(manifest), true)
	if err != nil {
		return nil, err
	}

	// verify namespace override if not done by kubeclient
	if err = h.overrideNamespace(resourceList, targetNamespace); err != nil {
		return nil, err
	}
	return resourceList, nil
}

func (h *HelmClient) PerformUpdate(existingResources, targetResources kube.ResourceList, force bool) (*kube.Result, error) {
	return h.kubeClient.Update(existingResources, targetResources, force)
}

func (h *HelmClient) PerformCreate(targetResources kube.ResourceList) (*kube.Result, error) {
	return h.kubeClient.Create(targetResources)
}

func (h *HelmClient) CheckWaitForResources(targetResources kube.ResourceList, actionClient *action.Install, operation HelmOperation) error {
	if actionClient.Wait && actionClient.Timeout != 0 {
		if operation == OperationDelete {
			return h.kubeClient.WaitForDelete(targetResources, h.waitTimeout)
		} else {
			if actionClient.WaitForJobs {
				return h.kubeClient.WaitWithJobs(targetResources, h.waitTimeout)
			} else {
				return h.kubeClient.Wait(targetResources, h.waitTimeout)
			}
		}
	}
	return nil
}

func (h *HelmClient) CheckReadyState(ctx context.Context, targetResources kube.ResourceList) (bool, error) {
	readyChecker := kube.NewReadyChecker(h.clientSet, func(format string, v ...interface{}) {}, kube.PausedAsReady(true), kube.CheckJobs(true))
	return h.checkReady(ctx, targetResources, readyChecker)
}

func (h *HelmClient) setNamespaceIfNotPresent(targetNamespace string, resourceInfo *resource.Info, helper *resource.Helper, runtimeObject runtime.Object) error {
	// check if resource is scoped to namespaces
	if helper.NamespaceScoped && resourceInfo.Namespace == "" {
		// check existing namespace - continue only if not set
		if targetNamespace == "" {
			targetNamespace = v1.NamespaceDefault
		}

		// set namespace on request
		resourceInfo.Namespace = targetNamespace
		metaObject, err := meta.Accessor(runtimeObject)
		if err != nil {
			return err
		}

		// set namespace on runtime object
		metaObject.SetNamespace(targetNamespace)
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

func (h *HelmClient) checkReady(ctx context.Context, resourceList kube.ResourceList, readyChecker kube.ReadyChecker) (bool, error) {
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
