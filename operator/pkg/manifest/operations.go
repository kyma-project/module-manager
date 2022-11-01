package manifest

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)

// ChartInfo defines helm chart information.
type ChartInfo struct {
	ChartPath   string
	RepoName    string
	URL         string
	ChartName   string
	ReleaseName string
	Flags       types.ChartFlags
}

// InstallInfo represents deployment information artifacts to be processed.
type InstallInfo struct {
	// ChartInfo represents chart information to be processed
	*ChartInfo
	// ResourceInfo represents additional resources to be processed
	ResourceInfo
	// ClusterInfo represents target cluster information
	types.ClusterInfo
	// Ctx hold the current context
	Ctx context.Context //nolint:containedctx
	// CheckFn returns a boolean indicating ready state based on custom checks
	CheckFn custom.CheckFnType
	// CheckReadyStates indicates if native resources should be checked for ready states
	CheckReadyStates bool
	// UpdateRepositories indicates if repositories should be updated
	UpdateRepositories bool
}

// ResourceInfo represents additional resources.
type ResourceInfo struct {
	// BaseResource represents base custom resource that is being reconciled
	BaseResource *unstructured.Unstructured
	// CustomResources represents a set of additional custom resources to be installed
	CustomResources []*unstructured.Unstructured
	// Crds represents a set of additional custom resource definitions to be installed
	Crds []*apiextensions.CustomResourceDefinition
}

type ResponseChan chan *InstallResponse

//nolint:errname
type InstallResponse struct {
	Ready             bool
	ChartName         string
	Flags             types.ChartFlags
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *InstallResponse) Error() string {
	return r.Err.Error()
}

type Operations struct {
	logger             *logr.Logger
	helmClient         types.HelmClient
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
}

func InstallChart(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.HelmClientCache,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return operations.install(deployInfo)
}

func UninstallChart(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.HelmClientCache,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return operations.uninstall(deployInfo)
}

func ConsistencyCheck(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.HelmClientCache,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return operations.consistencyCheck(deployInfo)
}

func newOperations(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.HelmClientCache,
) (*Operations, error) {
	var cacheKey client.ObjectKey

	if deployInfo.BaseResource != nil {
		// cache HelmClient by Kyma name
		// as there can be multiple Manifests belonging to the same Kyma resource
		label, err := util.GetResourceLabel(deployInfo.BaseResource, labels.ComponentOwner)
		if err != nil {
			return nil, err
		}
		cacheKey = client.ObjectKey{Name: label, Namespace: deployInfo.BaseResource.GetNamespace()}
	}
	var helmClient types.HelmClient
	if cache != nil {
		// read HelmClient from cache
		helmClient = cache.Get(cacheKey)
	}
	if helmClient == nil {
		memCacheClient, err := getMemCacheClient(deployInfo.Config)
		if err != nil {
			return nil, err
		}
		helmClient, err = getHelmClient(cacheKey, deployInfo, cache, memCacheClient, logger)
		if err != nil {
			return nil, err
		}
		// cache HelmClient
		if cache != nil {
			cache.Set(cacheKey, helmClient)
		}
	}

	operations := &Operations{
		logger:             logger,
		helmClient:         helmClient,
		flags:              deployInfo.Flags,
		resourceTransforms: resourceTransforms,
	}

	return operations, nil
}

// getHelmClient returns a new HelmClient instance and caches it in-memory.
func getHelmClient(cacheKey client.ObjectKey, deployInfo InstallInfo, cache types.HelmClientCache,
	memCacheClient discovery.CachedDiscoveryInterface, logger *logr.Logger,
) (types.HelmClient, error) {
	// create RESTGetter with cached memcached client
	restGetter := manifestRest.NewRESTClientGetter(deployInfo.Config, memCacheClient)

	// use deferred discovery client here as GVs applicable to the client are inconsistent at this moment
	discoveryMapper := restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient)

	// create HelmClient instance
	helmClient, err := NewHelmClient(restGetter, discoveryMapper, deployInfo.Config, cli.New(),
		deployInfo.ReleaseName, deployInfo.Flags, logger)
	if err != nil {
		return nil, err
	}

	return helmClient, nil
}

// getMemCacheClient creates and returns a new instance of MemCacheClient.
func getMemCacheClient(config *rest.Config) (discovery.CachedDiscoveryInterface, error) {
	// The more groups you have, the more discovery requests you need to make.
	// given 25 groups (our groups + a few custom conf) with one-ish version each, discovery needs to make 50 requests
	// double it just so we don't end up here again for a while.  This config is only used for discovery.
	config.Burst = 100
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(discoveryClient), nil
}

func (o *Operations) getClusterResources(deployInfo InstallInfo, operation types.HelmOperation,
) (types.ResourceLists, error) {
	manifest, err := o.getManifestForChartPath(deployInfo, o.flags)
	if err != nil {
		return types.ResourceLists{}, err
	}

	NsResourceList, err := o.helmClient.GetNsResource()
	if err != nil {
		return types.ResourceLists{}, err
	}

	targetResourceList, err := o.helmClient.GetTargetResources(deployInfo.Ctx, manifest,
		o.resourceTransforms, deployInfo.BaseResource)
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
		Namespace: NsResourceList,
	}, nil
}

func (o *Operations) consistencyCheck(deployInfo InstallInfo) (bool, error) {
	// verify CRDs
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		false); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// verify CR
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, deployInfo.ClusterInfo.Client,
		false); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// verify manifest resources - by count
	// TODO: better strategy for resource verification?
	resourceLists, err := o.getClusterResources(deployInfo, "")
	if err != nil {
		return false, errors.Wrap(err, "could not render current resources from manifest")
	}
	if len(resourceLists.Target) > len(resourceLists.Installed) {
		return false, nil
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *Operations) install(deployInfo InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		true); err != nil {
		return false, err
	}

	// render manifests
	resourceLists, err := o.getClusterResources(deployInfo, types.OperationCreate)
	if err != nil {
		return false, err
	}

	// install resources
	if _, err = o.installResources(resourceLists); err != nil {
		return false, err
	}

	// verify resource installation
	if ready, err := o.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationCreate); !ready || err != nil {
		return ready, err
	}

	o.logger.Info("Install Complete!! Happy Manifesting!", "release", deployInfo.ReleaseName,
		"chart", deployInfo.ChartName)

	// update Helm repositories
	if deployInfo.UpdateRepositories {
		if err = o.helmClient.UpdateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	// install crs - if present do not update!
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, deployInfo.Client,
		true); err != nil {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *Operations) installResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	if resourceLists.Installed == nil && len(resourceLists.Target) > 0 {
		return o.helmClient.PerformCreate(resourceLists)
	}

	return o.helmClient.PerformUpdate(resourceLists, true)
}

func (o *Operations) verifyResources(ctx context.Context, resourceLists types.ResourceLists, verifyReadyStates bool,
	operationType types.HelmOperation,
) (bool, error) {
	return o.helmClient.CheckWaitForResources(ctx, resourceLists.GetWaitForResources(), operationType,
		verifyReadyStates)
}

func (o *Operations) uninstallResources(resourceLists types.ResourceLists) error {
	count, err := o.helmClient.PerformDelete(resourceLists)
	if err != nil {
		return err
	}
	o.logger.Info("component deletion executed", "resource count", count)
	return nil
}

func (o *Operations) uninstall(deployInfo InstallInfo) (bool, error) {
	resourceLists, err := o.getClusterResources(deployInfo, types.OperationDelete)
	if err != nil {
		return false, err
	}

	// delete crs first - proceed only if not found
	// proceed if CR type doesn't exist anymore - since associated CRDs might be deleted from resource uninstallation
	// since there might be a deletion process to be completed by other manifest resources
	deleted, err := resource.RemoveCRs(deployInfo.Ctx, deployInfo.CustomResources,
		deployInfo.ClusterInfo.Client)
	if !meta.IsNoMatchError(err) && (err != nil || !deleted) {
		return false, err
	}

	// uninstall resources
	if err := o.uninstallResources(resourceLists); err != nil {
		return false, err
	}

	// verify resource uninstallations
	if ready, err := o.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationDelete); !ready || err != nil {
		return ready, err
	}

	// update Helm repositories
	if deployInfo.UpdateRepositories {
		if err = o.helmClient.UpdateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	// delete crds first - if not present ignore!
	if err := resource.RemoveCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client); err != nil {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, err
}

func (o *Operations) getManifestForChartPath(deployInfo InstallInfo, flags types.ChartFlags) (string, error) {
	var err error
	helmRepo := false
	chartPath := deployInfo.ChartPath
	if chartPath == "" {
		// 1. legacy case - helm repo
		helmRepo = true
		chartPath, err = o.helmClient.DownloadChart(deployInfo.RepoName, deployInfo.URL, deployInfo.ChartName)
		if err != nil {
			return "", err
		}
	} else {
		// 2. OCI Image
		if renderedManifest, err := o.handleRenderedManifestForStaticChart(deployInfo.ChartName, chartPath); err != nil {
			return "", err
		} else if renderedManifest != "" {
			return renderedManifest, nil
		}
	}
	o.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if rendered manifest doesn't exist
	stringifiedManifest, err := o.helmClient.RenderManifestFromChartPath(chartPath, flags.SetFlags)
	if err != nil {
		return "", err
	}

	// optional: Uncomment below to print manifest
	// fmt.Println(release.Manifest)

	// write rendered manifest file
	if !helmRepo {
		err = util.WriteToFile(util.GetFsManifestChartPath(chartPath), []byte(stringifiedManifest))
	}
	return stringifiedManifest, err
}

func (o *Operations) handleRenderedManifestForStaticChart(chartName, chartPath string) (string, error) {
	// verify chart path exists
	if _, err := os.Stat(chartPath); err != nil {
		return "", fmt.Errorf("locating chart %s at path %s resulted in an error: %w", chartName, chartPath, err)
	}
	o.logger.Info(fmt.Sprintf("chart dir %s found at path %s", chartName, chartPath))

	// check if rendered manifest already exists
	stringifiedManifest, err := util.GetStringifiedYamlFromFilePath(util.GetFsManifestChartPath(chartPath))
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("locating chart rendered manifest %s at path %s resulted in an error: %w",
				chartName, chartPath, err)
		}
		return "", nil
	}

	// return already rendered manifest here
	return stringifiedManifest, nil
}
