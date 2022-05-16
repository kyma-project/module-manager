package manifest

import (
	"bytes"
	"fmt"
	"github.com/kyma-project/manifest-operator/pkg/util"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/strvals"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"reflect"
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
}

func NewClient(kubeClient *kube.Client, settings *cli.EnvSettings) *HelmClient {
	return &HelmClient{
		kubeClient: kubeClient,
		settings:   settings,
	}
}

func (h *HelmClient) getGenericConfig(namespace string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)
	clientGetter := genericclioptions.NewConfigFlags(false)
	clientGetter.Namespace = &namespace
	if err := actionConfig.Init(clientGetter, namespace, "secrets", func(format string, v ...interface{}) {
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
	actionClient.Atomic = true
	actionClient.Wait = true
	actionClient.CreateNamespace = true
	actionClient.DryRun = true
	actionClient.Replace = true     // Skip the name check
	actionClient.IncludeCRDs = true //include CRDs in the templated output
	actionClient.ClientOnly = true
	actionClient.ReleaseName = releaseName

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
	// create namespace
	if actionClient.CreateNamespace {
		buf, err := util.GetNamespaceObjBytes(actionClient.Namespace)
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
		}
	}
	return nil
}

func (h *HelmClient) GetTargetResources(manifest string) (kube.ResourceList, error) {
	return h.kubeClient.Build(bytes.NewBufferString(manifest), true)
}

func (h *HelmClient) PerformUpdate(existingResources, targetResources kube.ResourceList, force bool) (*kube.Result, error) {
	return h.kubeClient.Update(existingResources, targetResources, force)
}

func (h *HelmClient) PerformCreate(targetResources kube.ResourceList) (*kube.Result, error) {
	return h.kubeClient.Create(targetResources)
}
