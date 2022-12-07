package manifest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	manifestTypes "github.com/kyma-project/module-manager/pkg/client"

	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
)

type helm struct {
	clients         *manifestTypes.SingletonClients
	settings        *cli.EnvSettings
	repoHandler     *RepoHandler
	logger          logr.Logger
	forceCRDRemoval bool
	*Rendered
}

// verify compliance of interface.
var _ types.ManifestClient = &helm{}

//nolint:gochecknoglobals
var accessor = meta.NewAccessor()

// NewHelmProcessor returns a new instance of the helm processor.
// The returned helm instance contains necessary clients based on rest config and rest mapper,
// combined with helm configuration, like helm native flags and --set flags.
// Additionally, it also transforms the manifest resources based on user defined input.
// On the returned helm instance, installation, uninstallation and verification checks
// can be executed on the resource manifest.
func NewHelmProcessor(clients *manifestTypes.SingletonClients, settings *cli.EnvSettings,
	logger logr.Logger, render *Rendered, deployInfo *types.InstallInfo, forceCRDRemoval bool,
) (types.ManifestClient, error) {
	helmClient := &helm{
		clients:         clients,
		logger:          logger,
		repoHandler:     NewRepoHandler(logger, settings),
		settings:        settings,
		Rendered:        render,
		forceCRDRemoval: forceCRDRemoval,
	}

	// verify compliance of interface
	var helmProcessor types.ManifestClient = helmClient

	// always override existing flags config
	// to ensure CR updates are reflected on the action client
	if err := helmClient.resetFlags(deployInfo); err != nil {
		return nil, err
	}

	return helmProcessor, nil
}

// GetRawManifest returns processed resource manifest using helm client.
func (h *helm) GetRawManifest(info *types.InstallInfo) *types.ParsedFile {
	chartPath, err := h.resolveChartPath(info)
	if err != nil {
		return types.NewParsedFile("", err)
	}
	h.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if Rendered manifest doesn't exist
	// check newly Rendered manifest here
	return types.NewParsedFile(h.renderReleaseFromChartPath(info.Ctx, chartPath, info.Flags.SetFlags))
}

func (h *helm) resolveChartPath(info *types.InstallInfo) (string, error) {
	chartPath := info.ChartPath
	if chartPath == "" {
		var err error
		// legacy case - download chart from helm repo
		chartPath, err = h.downloadChart(info.RepoName, info.URL, info.ChartName)
		if err != nil {
			return "", err
		}
	}
	return chartPath, nil
}

// Install transforms and applies Helm based manifest using helm client.
func (h *helm) Install(stringifedManifest string, info *types.InstallInfo, transforms []types.ObjectTransform,
	postRuns []types.PostRun,
) (bool, error) {
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, info, transforms, true)
	if err != nil {
		return false, err
	}

	// install resources
	result, err := h.installResources(resourceLists, false)
	if err != nil {
		return false, err
	}

	for i := range postRuns {
		if err := postRuns[i](
			info.Ctx, info.Client, info.ResourceInfo.BaseResource, resourceLists,
		); err != nil {
			return false, fmt.Errorf("post-run %v failed: %w", i, err)
		}
	}

	h.logger.V(util.DebugLogLevel).Info("installed | updated Helm chart resources",
		"create count", len(result.Created),
		"update count", len(result.Updated),
		"chart", info.ChartName,
		"release", info.ReleaseName,
		"resource", client.ObjectKeyFromObject(info.BaseResource).String())

	// verify resources
	if err := h.checkTargetResources(info.Ctx, resourceLists.Target,
		types.OperationCreate, info.CheckReadyStates); err != nil {
		return false, err
	}

	// update helm repositories
	if info.UpdateRepositories {
		if err = h.updateRepos(info.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

// Uninstall transforms and deletes Helm based manifest using helm client.
func (h *helm) Uninstall(stringifedManifest string, info *types.InstallInfo, transforms []types.ObjectTransform,
	postRuns []types.PostRun,
) (bool, error) {
	// convert for Helm processing
	// do not retry since if the Kind is not existing anymore, there can never be a resource left in the cluster
	// this means we can continue the installation
	// resourceLists will not contain any resource.Infos that are not possible to use with the cluster
	resourceLists, err := h.parseToResourceLists(stringifedManifest, info, transforms, false)

	// here we verify that out of all errors, the error that we are seeing here is actually an aggregate
	// this is because for every resource there can be at least one error
	// however due to being an uninstallation, we can continue for NoKindMatchErrors.
	// thus, if all errors are of type meta.NoKindMatchError, we can simply continue the uninstallation.
	var multiErr *types.MultiError
	if ok := errors.As(err, &multiErr); ok {
		for i := range multiErr.Errs {
			var noMatchErr *meta.NoKindMatchError
			if isNoKindMatch := errors.As(multiErr.Errs[i], &noMatchErr); !isNoKindMatch {
				return false, multiErr.Errs[i]
			}
		}
	}

	// uninstall resources
	_, err = h.uninstallResources(resourceLists)
	if err != nil {
		return false, err
	}

	for i := range postRuns {
		if err := postRuns[i](
			info.Ctx, info.Client, info.ResourceInfo.BaseResource, resourceLists,
		); err != nil {
			return false, fmt.Errorf("post-run %v failed: %w", i, err)
		}
	}

	h.logger.V(util.DebugLogLevel).Info("uninstalled Helm chart resources",
		"chart", info.ChartName,
		"release", info.ReleaseName,
		"resource", client.ObjectKeyFromObject(info.BaseResource).String())

	// verify resource uninstallation
	if err := h.checkTargetResources(
		info.Ctx, resourceLists.Target, types.OperationDelete,
		info.CheckReadyStates); err != nil {
		return false, err
	}

	// include CRDs means that the Chart will include the CRDs during rendering, if this is set to false,
	// we go into the cluster on our own and remove the CRDs since the uninstallation will not have included
	// the CRDs (as they were not part of the original render)
	// if CRDRemoval is forced, it will overwrite the IncludeCRDs flag, but if you do already include CRDs
	// in the rendering, the uninstallation will also try to delete these resources.
	if h.forceCRDRemoval || !h.clients.Install().IncludeCRDs {
		// delete all crds located in the original helm chart
		// WARNING: this can be dangerous if another operator is relying on CRDs here!
		// If the chart is removed while another chart is depending on it, it can cause havoc!
		// For more info, see
		// https://helm.sh/docs/chart_best_practices/custom_resource_definitions/#some-caveats-and-explanations
		if err := h.uninstallChartCRDs(info); err != nil {
			return false, err
		}
	}

	// update Helm repositories
	if info.UpdateRepositories {
		if err = h.updateRepos(info.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

func isHelmClientNotFound(err error) bool {
	// Refactoring this error check after this PR get merged and released https://github.com/helm/helm/pull/11591
	if err != nil && strings.Contains(err.Error(), "object not found, skipping delete") {
		return true
	}
	return false
}

// uninstallChartCRDs uses a types.InstallInfo to lookup a chart and then tries to remove all CRDs found within it
// and its dependencies. It can be used to forcefully remove all CRDs from a chart when CRDs were not included in the
// render. Unlike our optimized installation, this one is built sequentially as we do not need the performance
// here.
func (h *helm) uninstallChartCRDs(info *types.InstallInfo) error {
	chartPath, err := h.resolveChartPath(info)
	if err != nil {
		return err
	}
	loadedChart, err := h.repoHandler.LoadChart(chartPath, h.clients.Install())
	if err != nil {
		return err
	}
	crds, err := h.crdsFromChart(loadedChart)
	if err != nil {
		return err
	}
	resList, err := h.getTargetResources(info.Ctx, crds.String(), nil, nil, false)
	if err != nil {
		return err
	}

	_, deleteErrs := h.clients.KubeClient().Delete(resList)
	if len(deleteErrs) > 0 {
		filteredErrs := filterNotFoundError(deleteErrs)
		if len(filteredErrs) > 0 {
			return types.NewMultiError(filteredErrs)
		}
	}

	return nil
}

// IsConsistent indicates if helm installation is consistent with the desired manifest resources.
func (h *helm) IsConsistent(stringifedManifest string, info *types.InstallInfo,
	transforms []types.ObjectTransform, postRuns []types.PostRun,
) (bool, error) {
	startConsistencyCheck := time.Now()

	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, info, transforms, true)
	if err != nil {
		return false, err
	}

	if len(resourceLists.Target) != len(resourceLists.Installed) {
		h.logger.Info("consistency check noticed a difference between installed "+
			"and target resources and will attempt to repair this",
			"chart", info.ChartName,
			"release", info.ReleaseName,
			"resource", client.ObjectKeyFromObject(info.BaseResource).String())
	}

	// install resources without force, it will lead to 3 way merge / JSON apply patches
	result, err := h.installResources(resourceLists, false)
	if err != nil {
		return false, err
	}

	for i := range postRuns {
		if err := postRuns[i](
			info.Ctx, info.Client, info.ResourceInfo.BaseResource, resourceLists,
		); err != nil {
			return false, fmt.Errorf("post-run %v failed: %w", i, err)
		}
	}

	// verify resources
	if err := h.checkTargetResources(info.Ctx,
		resourceLists.Target,
		types.OperationCreate,
		info.CheckReadyStates); err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("consistency check finished",
		"create count", len(result.Created),
		"update count", len(result.Updated),
		"chart", info.ChartName,
		"release", info.ReleaseName,
		"resource", client.ObjectKeyFromObject(info.BaseResource).String(),
		"time", time.Since(startConsistencyCheck).String())

	// update helm repositories
	if info.UpdateRepositories {
		if err = h.updateRepos(info.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (h *helm) installResources(resourceLists types.ResourceLists, force bool) (*kube.Result, error) {
	// create namespace resource first!
	if len(resourceLists.Namespace) > 0 {
		if _, err := h.clients.KubeClient().Create(resourceLists.Namespace); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
	}

	// fresh install
	if resourceLists.Installed == nil && len(resourceLists.Target) > 0 {
		return h.clients.KubeClient().Create(resourceLists.Target)
	}

	// missing resources - update with 3 way merge
	return h.clients.KubeClient().Update(resourceLists.Installed, resourceLists.Target, force)
}

func (h *helm) uninstallResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	var response *kube.Result
	var deleteErrs []error
	if resourceLists.Installed != nil {
		response, deleteErrs = h.clients.KubeClient().Delete(resourceLists.Installed)
		if len(deleteErrs) > 0 {
			filteredErrs := filterNotFoundError(deleteErrs)
			if len(filteredErrs) > 0 {
				return nil, types.NewMultiError(filteredErrs)
			}
		}
	}
	return response, nil
}

func filterNotFoundError(delErrors []error) []error {
	wrappedErrors := make([]error, 0)
	for _, err := range delErrors {
		if isHelmClientNotFound(err) || apierrors.IsNotFound(err) {
			continue
		}
		wrappedErrors = append(wrappedErrors, err)
	}
	return wrappedErrors
}

func (h *helm) checkTargetResources(ctx context.Context,
	targetResources kube.ResourceList,
	operation types.HelmOperation,
	verifyWithoutTimeout bool) error {
	// verifyWithoutTimeout flag checks native resources are in their respective ready states
	// without a timeout defined
	if verifyWithoutTimeout {
		if operation == types.OperationDelete {
			return checkResourcesDeleted(targetResources)
		}
		clientSet, err := h.clients.KubernetesClientSet()
		if err != nil {
			return err
		}
		readyChecker := kube.NewReadyChecker(clientSet,
			func(format string, args ...interface{}) {
				h.logger.V(util.DebugLogLevel).Info(fmt.Sprintf(format, args...))
			},
			kube.PausedAsReady(true),
			kube.CheckJobs(true))

		return checkReady(ctx, targetResources, readyChecker)
	}

	// if Wait or WaitForJobs is enabled, resources are verified to be in ready state with a timeout
	if !h.clients.Install().Wait || h.clients.Install().Timeout == 0 {
		// return here as ready, since waiting flags were not set
		return nil
	}

	if operation == types.OperationDelete {
		// WaitForDelete reports an error if resources are not deleted in the specified timeout
		return h.clients.KubeClient().WaitForDelete(targetResources, h.clients.Install().Timeout)
	}

	if h.clients.Install().WaitForJobs {
		// WaitWithJobs reports an error if resources are not deleted in the specified timeout
		return h.clients.KubeClient().WaitWithJobs(targetResources, h.clients.Install().Timeout)
	}
	// Wait reports an error if resources are not deleted in the specified timeout
	return h.clients.KubeClient().Wait(targetResources, h.clients.Install().Timeout)
}

func (h *helm) updateRepos(ctx context.Context) error {
	return h.repoHandler.Update(ctx)
}

func (h *helm) resetFlags(deployInfo *types.InstallInfo) error {
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
	return h.clients.Install().ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *helm) renderReleaseFromChartPath(ctx context.Context, chartPath string, flags types.Flags) (string, error) {
	// if Rendered manifest doesn't exist
	chartRequested, err := h.repoHandler.LoadChart(chartPath, h.clients.Install())
	if err != nil {
		return "", err
	}

	// include CRDs means that the Chart will include the CRDs during rendering, if this is set to false,
	// we imitate HELM Action client behavior by first installing CRDs and then rendering them
	if !h.clients.Install().IncludeCRDs {
		// rendering requires CRDs to be installed so that any resources in the possibly rendered chart can be found
		err = h.optimizedInstallCRDs(ctx, chartRequested)
		if err != nil {
			return "", err
		}
	}

	// retrieve manifest
	release, err := h.clients.Install().Run(chartRequested, flags)
	if err != nil {
		return "", err
	}

	return release.Manifest, nil
}

// optimizedInstallCRDs is oriented on action.installCRDs, an internal method from HELM used for installing CRDs.
// Usually, with a rendered release, this method gets called in front of the render to install dependencies before
// resolving REST Mappings for kubernetes. However, since we do not use releases, but dry-run, we install CRDs
// in advance.
// There are 3 key differences between the method in the HELM client and this one
// First, it resets the singleton client, allowing us to only reset the Mapper once per render process.
// Second, it parses the manifest string based on the existing methods available in the HELM client.
// Third, it creates crds and collects requirements concurrently to speed up CRD creation.
func (h *helm) optimizedInstallCRDs(ctx context.Context, chartRequested *chart.Chart) error {
	crds, err := h.crdsFromChart(chartRequested)
	if err != nil {
		return err
	}

	resList, err := h.getTargetResources(ctx, crds.String(), nil, nil, false)
	if err != nil {
		return err
	}

	crdInstallWaitGroup := sync.WaitGroup{}
	errors := make(chan error, len(resList))
	createCRD := func(i int) {
		defer crdInstallWaitGroup.Done()
		_, err := h.clients.KubeClient().Create(kube.ResourceList{resList[i]})
		errors <- err
	}

	for i := range resList {
		crdInstallWaitGroup.Add(1)
		go createCRD(i)
	}
	crdInstallWaitGroup.Wait()
	close(errors)

	for err := range errors {
		if err == nil || apierrors.IsAlreadyExists(err) {
			continue
		}
		h.logger.Error(err, "failed on crd installation as pre-requisite for helm rendering")
		return err
	}

	restMapper, err := h.clients.ToRESTMapper()
	if err != nil {
		return err
	}
	meta.MaybeResetRESTMapper(restMapper)

	return nil
}

func (h *helm) crdsFromChart(chart *chart.Chart) (*bytes.Buffer, error) {
	crds := chart.CRDObjects()
	// transform the stringified manifest into our resource list the same way we do for other resources
	// but without any custom transforms
	var crdManifest bytes.Buffer
	for i := range crds {
		crdManifest.Write(append(bytes.TrimPrefix(crds[i].File.Data, []byte("---\n")), '\n'))
	}
	return &crdManifest, nil
}

func (h *helm) setDefaultFlags(releaseName string) {
	h.clients.Install().DryRun = true
	h.clients.Install().Atomic = false

	h.clients.Install().WaitForJobs = false

	h.clients.Install().Replace = true // Skip the name check
	h.clients.Install().IncludeCRDs = false
	h.clients.Install().UseReleaseName = false
	h.clients.Install().ReleaseName = releaseName

	// ClientOnly has no interaction with the API server
	// So unless mentioned no additional API Versions can be used as part of helm chart installation
	h.clients.Install().ClientOnly = false

	h.clients.Install().Namespace = v1.NamespaceDefault
	// this will prohibit resource conflict validation while uninstalling
	h.clients.Install().IsUpgrade = true

	// default versioning if unspecified
	if h.clients.Install().Version == "" && h.clients.Install().Devel {
		h.clients.Install().Version = ">0.0.0-0"
	}
}

func (h *helm) setCustomFlags(flags types.ChartFlags) error {
	clientValue := reflect.Indirect(reflect.ValueOf(h.clients.Install()))

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

// parseToResourceLists opinionates manifest.yaml transformation based on installInfo and custom transforms
// after the reosurceList was parsed.
// It also supports a flag to delegate no match error resilience to the lookup of the clients used to later on
// interact with the cluster. This is useful in case you want to transform a list of resources, but you know
// that some resources might be of a Kind that is not supported in the cluster (e.g. because it was already uninstalled)
// By specifying retryOnNoMatch => false, you are able to handle noMatch errors on your own, not causing unnecessary
// and costly resets.
func (h *helm) parseToResourceLists(stringifiedManifest string, deployInfo *types.InstallInfo,
	transforms []types.ObjectTransform, retryOnNoMatch bool,
) (types.ResourceLists, error) {
	nsResourceList, err := h.GetNsResource()
	if err != nil {
		return types.ResourceLists{Namespace: nsResourceList}, err
	}

	targetResourceList, targetError := h.getTargetResources(deployInfo.Ctx, stringifiedManifest,
		transforms, deployInfo.BaseResource, retryOnNoMatch,
	)

	existingResourceList, filterErr := util.FilterExistingResources(targetResourceList)

	list := types.ResourceLists{
		Target:    targetResourceList,
		Installed: existingResourceList,
		Namespace: nsResourceList,
	}

	if filterErr != nil {
		return list, fmt.Errorf("could not filter existing resources from manifest: %w", filterErr)
	}

	if targetError != nil {
		return list, fmt.Errorf("could not render target resources from manifest: %w", targetError)
	}

	return list, nil
}

func (h *helm) GetNsResource() (kube.ResourceList, error) {
	// set kubeclient namespace for override
	// TODO remove setting in kubeclient
	h.clients.KubeClient().Namespace = h.clients.Install().Namespace

	// validate namespace parameters
	// proceed only if not default namespace since it already exists
	if !h.clients.Install().CreateNamespace || h.clients.Install().Namespace == v1.NamespaceDefault {
		return nil, nil
	}

	ns := h.clients.Install().Namespace
	nsBuf, err := util.GetNamespaceObjBytes(ns)
	if err != nil {
		return nil, err
	}
	return h.clients.KubeClient().Build(bytes.NewBuffer(nsBuf), false)
}

func (h *helm) getTargetResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject, retryOnNoMatch bool,
) (kube.ResourceList, error) {
	resourceList, err := h.resourceListFromManifest(ctx, manifest, transforms, object, retryOnNoMatch)

	// verify namespace override if not done by kubeclient
	if err := overrideNamespace(resourceList, h.clients.Install().Namespace); err != nil {
		return nil, err
	}

	return resourceList, err
}

func (h *helm) resourceListFromManifest(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject, retryOnNoMatch bool,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	objects, err := util.Transform(ctx, manifest, object, transforms)
	if err != nil {
		return nil, err
	}

	errs := make([]error, 0, len(objects.Items))
	for _, unstructuredObject := range objects.Items {
		resourceInfo, err := h.clients.ResourceInfo(unstructuredObject, retryOnNoMatch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resourceList = append(resourceList, resourceInfo)
	}
	if len(errs) > 0 {
		return resourceList, types.NewMultiError(errs)
	}
	return resourceList, nil
}

// InvalidateConfigAndRenderedManifest compares the cached hash with the processed hash for helm flags.
// If the hashes are not equal it resets the flags on the helm action client.
// Also, it deletes the persisted manifest resource on the file system at <chartPath>/manifest/manifest.yaml.
func (h *helm) InvalidateConfigAndRenderedManifest(deployInfo *types.InstallInfo, cachedHash uint32) (uint32, error) {
	newHash, err := util.CalculateHash(deployInfo.Flags)
	if err != nil {
		return 0, err
	}
	// no changes
	if newHash == cachedHash {
		return 0, nil
	}
	// not a new entry + hash change
	if cachedHash != 0 && newHash != cachedHash {
		// delete rendered manifest if previous config hash was found
		if parsedFile := h.DeleteCachedResources(deployInfo.ChartPath); parsedFile.GetRawError() != nil {
			// errors os.IsNotExist and os.IsPermission are ignored
			// since cached resources will be re-created if it doesn't exist
			// and resources are not cached at all if not permitted
			return 0, parsedFile
		}
		// if cached and new hash doesn't match, reset flag
		return newHash, h.resetFlags(deployInfo)
	}
	// new entry
	return newHash, nil
}

func (h *helm) GetClusterInfo() (types.ClusterInfo, error) {
	restConfig, err := h.clients.ToRESTConfig()
	if err != nil {
		return types.ClusterInfo{}, err
	}
	return types.ClusterInfo{
		Client: h.clients,
		Config: restConfig,
	}, nil
}
