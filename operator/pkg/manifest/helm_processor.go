package manifest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"

	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type helm struct {
	kubeClient   *kube.Client
	settings     *cli.EnvSettings
	restGetter   *manifestRest.ManifestRESTClientGetter
	clientSet    *kubernetes.Clientset
	restConfig   *rest.Config
	mapper       *restmapper.DeferredDiscoveryRESTMapper
	actionClient *action.Install
	repoHandler  *RepoHandler
	logger       *logr.Logger
	*transformer
	*rendered
}

// verify compliance of interface
var _ types.RenderSrc = &helm{}

//nolint:gochecknoglobals
var accessor = meta.NewAccessor()

// NewHelmProcessor returns a new instance of the helm processor.
// The returned helm instance contains necessary clients based on rest config and rest mapper,
// combined with helm configuration, like helm native flags and --set flags.
// Additionally, it also transforms the manifest resources based on user defined input.
// On the returned helm instance, installation, uninstallation and verification checks
// can be executed on the resource manifest.
func NewHelmProcessor(restGetter *manifestRest.ManifestRESTClientGetter,
	discoveryMapper *restmapper.DeferredDiscoveryRESTMapper, restConfig *rest.Config, settings *cli.EnvSettings,
	logger *logr.Logger, render *rendered, txformer *transformer) (types.RenderSrc, error) {
	var err error
	helmClient := &helm{
		logger:      logger,
		repoHandler: NewRepoHandler(logger, settings),
		settings:    settings,
		restGetter:  restGetter,
		restConfig:  restConfig,
		mapper:      discoveryMapper,
		transformer: txformer,
		rendered:    render,
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

	// verify compliance of interface
	var helmProcessor types.RenderSrc = helmClient

	return helmProcessor, nil
}

// GetRawManifest returns processed resource manifest using helm client.
func (h *helm) GetRawManifest(deployInfo types.InstallInfo) (string, error) {
	// always override existing flags config
	// to ensure CR updates are reflected on the action client
	err := h.resetFlags(deployInfo)
	if err != nil {
		return "", err
	}

	chartPath := deployInfo.ChartPath
	if chartPath == "" {
		// legacy case - download chart from helm repo
		chartPath, err = h.downloadChart(deployInfo.RepoName, deployInfo.URL, deployInfo.ChartName)
		if err != nil {
			return "", err
		}
	}
	h.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if rendered manifest doesn't exist
	stringifiedManifest, err := h.renderManifestFromChartPath(chartPath, deployInfo.Flags.SetFlags)
	if err != nil {
		return "", err
	}

	// optional: Uncomment below to print manifest
	// fmt.Println(release.Manifest)
	return stringifiedManifest, err
}

// Install transforms and applies Helm based manifest using helm client.
func (h *helm) Install(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	// install resources
	result, err := h.installResources(resourceLists)
	if err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("installed | updated Helm chart resources",
		"create count", len(result.Created),
		"update count", len(result.Updated),
		"chart", deployInfo.ChartName,
		"release", deployInfo.ReleaseName,
		"resource", client.ObjectKeyFromObject(deployInfo.BaseResource).String())

	// verify resources
	if ready, err := h.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationCreate); !ready || err != nil {
		return ready, err
	}

	// update helm repositories
	if deployInfo.UpdateRepositories {
		if err = h.updateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

// Uninstall transforms and deletes Helm based manifest using helm client.
func (h *helm) Uninstall(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	// uninstall resources
	result, err := h.uninstallResources(resourceLists)
	if err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("uninstalled Helm chart resources",
		"count", len(result.Deleted),
		"chart", deployInfo.ChartName,
		"release", deployInfo.ReleaseName,
		"resource", client.ObjectKeyFromObject(deployInfo.BaseResource).String())

	// verify resource uninstallation
	if ready, err := h.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationDelete); !ready || err != nil {
		return ready, err
	}

	// update Helm repositories
	if deployInfo.UpdateRepositories {
		if err = h.updateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

// IsConsistent indicates if helm installation is consistent with the desired manifest resources.
func (h *helm) IsConsistent(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	// verify manifest resources - by count
	// TODO: better strategy for resource verification?
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	return len(resourceLists.Target) == len(resourceLists.Installed), nil
}

func (h *helm) verifyResources(ctx context.Context, resourceLists types.ResourceLists, verifyReadyStates bool,
	operationType types.HelmOperation,
) (bool, error) {
	return h.checkWaitForResources(ctx, resourceLists.GetWaitForResources(), operationType,
		verifyReadyStates)
}

func (h *helm) installResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	// create namespace resource first!
	if len(resourceLists.Namespace) > 0 {
		if _, err := h.kubeClient.Create(resourceLists.Namespace); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
	}

	// fresh install
	if resourceLists.Installed == nil && len(resourceLists.Target) > 0 {
		return h.kubeClient.Create(resourceLists.Target)
	}

	// missing resources - update with force
	return h.kubeClient.Update(resourceLists.Installed, resourceLists.Target, true)

}

func (h *helm) uninstallResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	var response *kube.Result
	delErrors := make([]error, 0)
	if resourceLists.Installed != nil {
		// add namespace to deleted resources
		response, delErrors = h.kubeClient.Delete(resourceLists.GetResourcesToBeDeleted())
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err := range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return nil, wrappedError
		}
	}
	return response, nil
}

func (h *helm) deleteNamespace(namespace kube.ResourceList) error {
	if _, delErrors := h.kubeClient.Delete(namespace); len(delErrors) > 0 {
		var wrappedError error
		for _, err := range delErrors {
			wrappedError = fmt.Errorf("%w", err)
		}
		return wrappedError
	}
	return nil
}

func (h *helm) checkWaitForResources(ctx context.Context, targetResources kube.ResourceList,
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

func (h *helm) updateRepos(ctx context.Context) error {
	return h.repoHandler.Update(ctx)
}

func (h *helm) resetFlags(deployInfo types.InstallInfo) error {
	// set preliminary flag defaults
	h.setDefaultFlags(deployInfo.ReleaseName)

	// set user defined flags
	return h.setCustomFlags(deployInfo.Flags)
}

func (h *helm) downloadChart(repoName, url, chartName string) (string, error) {
	err := h.repoHandler.Add(repoName, url)
	if err != nil {
		return "", err
	}
	return h.actionClient.ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *helm) renderManifestFromChartPath(chartPath string, flags types.Flags) (string, error) {
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

func (h *helm) newInstallActionClient(namespace string, restGetter *manifestRest.ManifestRESTClientGetter,
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

func (h *helm) getGenericConfig(namespace string, restGetter *manifestRest.ManifestRESTClientGetter,
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

func (h *helm) setDefaultFlags(releaseName string) {
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

func (h *helm) setCustomFlags(flags types.ChartFlags) error {
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

func (h *helm) parseToResourceLists(stringifiedManifest string, deployInfo types.InstallInfo,
	transforms []types.ObjectTransform) (types.ResourceLists, error) {
	nsResourceList, err := h.GetNsResource()
	if err != nil {
		return types.ResourceLists{}, err
	}

	targetResourceList, err := h.getTargetResources(deployInfo.Ctx, stringifiedManifest,
		transforms, deployInfo.BaseResource)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render resources from manifest: %w", err)
	}

	existingResourceList, err := util.FilterExistingResources(targetResourceList)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render existing resources from manifest: %w", err)
	}

	return types.ResourceLists{
		Target:    targetResourceList,
		Installed: existingResourceList,
		Namespace: nsResourceList,
	}, nil
}

func (h *helm) GetNsResource() (kube.ResourceList, error) {
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
	return h.kubeClient.Build(bytes.NewBuffer(nsBuf), false)
}

func (h *helm) getTargetResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	var err error

	if len(transforms) == 0 {
		resourceList, err = h.kubeClient.Build(bytes.NewBufferString(manifest), false)
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

func (h *helm) transformManifestResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	objects, err := h.Transform(ctx, manifest, object, transforms)
	if err != nil {
		return nil, err
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

func (h *helm) convertToInfo(unstructuredObj *unstructured.Unstructured) (*resource.Info, error) {
	//TODO:  manual invalidation of mem cache client to maintain current state of server mapping for API resources
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

func (h *helm) assignRestMapping(gvk schema.GroupVersionKind, info *resource.Info) error {
	restMapping, err := h.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		h.mapper.Reset()
		return err
	}
	info.Mapping = restMapping
	return nil
}
