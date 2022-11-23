package manifest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
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

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type helm struct {
	clients     *manifestTypes.SingletonClients
	settings    *cli.EnvSettings
	repoHandler *RepoHandler
	logger      logr.Logger
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
	logger logr.Logger, render *Rendered, deployInfo types.InstallInfo,
) (types.ManifestClient, error) {
	helmClient := &helm{
		clients:     clients,
		logger:      logger,
		repoHandler: NewRepoHandler(logger, settings),
		settings:    settings,
		Rendered:    render,
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
func (h *helm) GetRawManifest(deployInfo types.InstallInfo) *types.ParsedFile {
	var err error
	chartPath := deployInfo.ChartPath
	if chartPath == "" {
		// legacy case - download chart from helm repo
		chartPath, err = h.downloadChart(deployInfo.RepoName, deployInfo.URL, deployInfo.ChartName)
		if err != nil {
			return types.NewParsedFile("", err)
		}
	}
	h.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if Rendered manifest doesn't exist
	// check newly Rendered manifest here
	return types.NewParsedFile(h.renderManifestFromChartPath(deployInfo.Ctx, chartPath, deployInfo.Flags.SetFlags))
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
	result, err := h.installResources(resourceLists, false)
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
	_, err = h.uninstallResources(resourceLists)
	if err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("uninstalled Helm chart resources",
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
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	// install resources without force, it will lead to 3 way merge / JSON apply patches
	result, err := h.installResources(resourceLists, false)
	if err != nil {
		return false, err
	}

	// verify resources
	ready, err := h.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationCreate)

	h.logger.V(util.DebugLogLevel).Info("consistency check",
		"consistent", ready,
		"create count", len(result.Created),
		"update count", len(result.Updated),
		"chart", deployInfo.ChartName,
		"release", deployInfo.ReleaseName,
		"resource", client.ObjectKeyFromObject(deployInfo.BaseResource).String())

	if !ready || err != nil {
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

func (h *helm) verifyResources(ctx context.Context, resourceLists types.ResourceLists, verifyReadyStates bool,
	operationType types.HelmOperation,
) (bool, error) {
	return h.checkWaitForResources(ctx, resourceLists.GetWaitForResources(), operationType,
		verifyReadyStates)
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
	var delErrors []error
	if resourceLists.Installed != nil {
		// add namespace to deleted resources
		response, delErrors = h.clients.KubeClient().Delete(resourceLists.GetResourcesToBeDeleted())
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

func (h *helm) checkWaitForResources(ctx context.Context, targetResources kube.ResourceList,
	operation types.HelmOperation, verifyWithoutTimeout bool,
) (bool, error) {
	// verifyWithoutTimeout flag checks native resources are in their respective ready states
	// without a timeout defined
	if verifyWithoutTimeout {
		if operation == types.OperationDelete {
			return checkResourcesDeleted(targetResources)
		}
		clientSet, err := h.clients.KubernetesClientSet()
		if err != nil {
			return false, err
		}
		readyChecker := kube.NewReadyChecker(clientSet,
			func(format string, args ...interface{}) {
				h.logger.V(util.DebugLogLevel).Info(format, args...)
			},
			kube.PausedAsReady(true),
			kube.CheckJobs(true))

		return checkReady(ctx, targetResources, readyChecker)
	}

	// if Wait or WaitForJobs is enabled, resources are verified to be in ready state with a timeout
	if !h.clients.Install().Wait || h.clients.Install().Timeout == 0 {
		// return here as ready, since waiting flags were not set
		return true, nil
	}

	if operation == types.OperationDelete {
		// WaitForDelete reports an error if resources are not deleted in the specified timeout
		return true, h.clients.KubeClient().WaitForDelete(targetResources, h.clients.Install().Timeout)
	}

	if h.clients.Install().WaitForJobs {
		// WaitWithJobs reports an error if resources are not deleted in the specified timeout
		return true, h.clients.KubeClient().WaitWithJobs(targetResources, h.clients.Install().Timeout)
	}
	// Wait reports an error if resources are not deleted in the specified timeout
	return true, h.clients.KubeClient().Wait(targetResources, h.clients.Install().Timeout)
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
	return h.clients.Install().ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *helm) renderManifestFromChartPath(ctx context.Context, chartPath string, flags types.Flags) (string, error) {
	// if Rendered manifest doesn't exist
	chartRequested, err := h.repoHandler.LoadChart(chartPath, h.clients.Install())
	if err != nil {
		return "", err
	}

	// rendering requires CRDs to be installed so that any resources in the possibly rendered chart can be found
	err = h.optimizedInstallCRDs(ctx, chartRequested)
	if err != nil {
		return "", err
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
	crds := chartRequested.CRDObjects()

	// transform the stringified manifest into our resource list the same way we do for other resources
	// but without any custom transforms
	var crdManifest bytes.Buffer
	for i := range crds {
		crdManifest.Write(append(bytes.TrimPrefix(crds[i].File.Data, []byte("---\n")), '\n'))
	}
	resList, err := h.getTargetResources(ctx, crdManifest.String(), nil, nil)
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

func (h *helm) parseToResourceLists(stringifiedManifest string, deployInfo types.InstallInfo,
	transforms []types.ObjectTransform,
) (types.ResourceLists, error) {
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
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	resourceList, err := h.transformManifestResources(ctx, manifest, transforms, object)
	if err != nil {
		return nil, err
	}

	// verify namespace override if not done by kubeclient
	if err = overrideNamespace(resourceList, h.clients.Install().Namespace); err != nil {
		return nil, err
	}

	return resourceList, nil
}

func (h *helm) transformManifestResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	objects, err := util.Transform(ctx, manifest, object, transforms)
	if err != nil {
		return nil, err
	}

	for _, unstructuredObject := range objects.Items {
		resourceInfo, err := h.clients.ResourceInfo(unstructuredObject)
		if err != nil {
			return nil, err
		}
		resourceList = append(resourceList, resourceInfo)
	}
	return resourceList, err
}

// InvalidateConfigAndRenderedManifest compares the cached hash with the processed hash for helm flags.
// If the hashes are not equal it resets the flags on the helm action client.
// Also, it deletes the persisted manifest resource on the file system at <chartPath>/manifest/manifest.yaml.
func (h *helm) InvalidateConfigAndRenderedManifest(deployInfo types.InstallInfo, cachedHash uint32) (uint32, error) {
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
