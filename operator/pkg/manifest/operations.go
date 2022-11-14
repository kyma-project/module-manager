package manifest

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type operations struct {
	logger             *logr.Logger
	renderSrc          types.RenderSrc
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
}

// InstallChart installs the resources based on types.InstallInfo and an appropriate rendering mechanism.
func InstallChart(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.install(deployInfo)
}

// UninstallChart uninstalls the resources based on types.InstallInfo and an appropriate rendering mechanism.
func UninstallChart(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.uninstall(deployInfo)
}

// ConsistencyCheck verifies consistency of resources based on types.InstallInfo and an appropriate rendering mechanism.
func ConsistencyCheck(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.consistencyCheck(deployInfo)
}

//nolint:revive
func NewOperations(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (*operations, error) {
	renderSrc, err := getRenderSrc(cache, deployInfo, logger)
	if err != nil {
		return nil, fmt.Errorf("unable to create manifest processor: %w", err)
	}

	ops := &operations{
		logger:             logger,
		renderSrc:          renderSrc,
		flags:              deployInfo.Flags,
		resourceTransforms: resourceTransforms,
	}

	return ops, nil
}

// getRenderSrc checks if the manifest processor client is cached and returns if available.
// If not available, it creates a new one based on deployInfo.
// Additionally, it verifies cached configuration for the manifest processor and invalidates it if required.
func getRenderSrc(cache types.RendererCache, deployInfo types.InstallInfo,
	logger *logr.Logger,
) (types.RenderSrc, error) {
	/* Manifest processor handling */
	clusterCacheKey := discoverCacheKey(deployInfo.BaseResource, logger)
	var renderSrc types.RenderSrc
	var err error
	if cache == nil || clusterCacheKey.Name == "" {
		// no processor entries
		return getManifestProcessor(deployInfo, logger)
	}

	// look for existing processor entries
	// read manifest renderer from processor
	if renderSrc = cache.GetProcessor(clusterCacheKey); renderSrc == nil {
		renderSrc, err = getManifestProcessor(deployInfo, logger)
		if err != nil {
			return nil, err
		}

		// update new manifest processor
		cache.SetProcessor(clusterCacheKey, renderSrc)
	}

	/* Configuration handling */
	// if there is no update on config - return from here
	nsNameBaseResource := client.ObjectKeyFromObject(deployInfo.BaseResource)
	configHash, err := renderSrc.InvalidateConfigAndRenderedManifest(deployInfo,
		cache.GetConfig(nsNameBaseResource))
	if err != nil {
		return nil, err
	}
	// no update on config - return from here
	if configHash == 0 && renderSrc != nil {
		return renderSrc, nil
	}

	// update hash config each time
	// e.g. in case of Helm the passed flags could lead to invalidation
	cache.SetConfig(nsNameBaseResource, configHash)
	// update manifest processor - since configuration could be reset
	cache.SetProcessor(clusterCacheKey, renderSrc)

	return renderSrc, nil
}

// discoverCacheKey returns processor key for caching of manifest renderer,
// by label value operator.kyma-project.io/processor-key.
// If label not found on base resource an empty processor key is returned.
func discoverCacheKey(resource client.Object, logger *logr.Logger) client.ObjectKey {
	if resource != nil {
		label, err := util.GetResourceLabel(resource, labels.CacheKey)
		var labelErr *util.LabelNotFoundError
		if errors.As(err, &labelErr) {
			logger.V(util.DebugLogLevel).Info("processor-key label missing, resource will not be cached. Resulted in",
				"error", err.Error(),
				"resource", client.ObjectKeyFromObject(resource))
		}
		// do not handle any other error if reported
		return client.ObjectKey{Name: label, Namespace: resource.GetNamespace()}
	}
	return client.ObjectKey{}
}

// getManifestProcessor returns a new types.RenderSrc instance
// this render source will handle subsequent operations for manifest resources based on types.InstallInfo.
func getManifestProcessor(deployInfo types.InstallInfo, logger *logr.Logger) (types.RenderSrc, error) {
	memCacheClient, err := getMemCacheClient(deployInfo.Config)
	if err != nil {
		return nil, err
	}
	render := NewRendered(logger)
	txformer := NewTransformer()
	// use deferred discovery client here as GVs applicable to the client are inconsistent at this moment
	discoveryMapper := restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient)

	chartKind, err := resource.GetChartKind(deployInfo)
	if err != nil {
		return nil, err
	}
	switch chartKind {
	case resource.HelmKind, resource.UnknownKind:
		// create RESTGetter with cached memcached client
		restGetter := manifestRest.NewRESTClientGetter(deployInfo.Config, memCacheClient)

		// create HelmClient instance
		return NewHelmProcessor(restGetter, discoveryMapper, logger, render, txformer, deployInfo)
	case resource.KustomizeKind:
		// create dynamic client for rest config
		dynamicClient, err := dynamic.NewForConfig(deployInfo.Config)
		if err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %w", err)
		}
		return NewKustomizeProcessor(dynamicClient, discoveryMapper, logger,
			render, txformer)
	}
	return nil, nil
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

func (o *operations) consistencyCheck(deployInfo types.InstallInfo) (bool, error) {
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

	// process manifest
	parsedFile := o.getManifestForChartPath(deployInfo)
	if parsedFile.GetRawError() != nil {
		return false, parsedFile
	}

	// consistency check
	consistent, err := o.renderSrc.IsConsistent(parsedFile.GetContent(), deployInfo, o.resourceTransforms)
	if err != nil || !consistent {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *operations) install(deployInfo types.InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		true); err != nil {
		return false, err
	}

	// process manifest
	parsedFile := o.getManifestForChartPath(deployInfo)
	if parsedFile.GetRawError() != nil {
		return false, parsedFile.GetRawError()
	}

	// install resources
	consistent, err := o.renderSrc.Install(parsedFile.GetContent(), deployInfo, o.resourceTransforms)
	if err != nil || !consistent {
		return false, err
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

func (o *operations) uninstall(deployInfo types.InstallInfo) (bool, error) {
	// delete crs first - proceed only if not found
	// proceed if CR type doesn't exist anymore - since associated CRDs might be deleted from resource uninstallation
	// since there might be a deletion process to be completed by other manifest resources
	deleted, err := resource.RemoveCRs(deployInfo.Ctx, deployInfo.CustomResources,
		deployInfo.ClusterInfo.Client)
	if !meta.IsNoMatchError(err) && (err != nil || !deleted) {
		return false, err
	}

	// process manifest
	parsedFile := o.getManifestForChartPath(deployInfo)
	if parsedFile.GetRawError() != nil {
		return false, parsedFile.GetRawError()
	}

	// uninstall resources
	consistent, err := o.renderSrc.Uninstall(parsedFile.GetContent(), deployInfo, o.resourceTransforms)
	if err != nil || !consistent {
		return false, err
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

func (o *operations) getManifestForChartPath(deployInfo types.InstallInfo) *types.ParsedFile {
	// 1. check provided manifest file
	// It is expected for deployInfo.ChartPath to contain ONE .yaml or .yml file,
	// which is assumed to contain a list of resources to be processed.
	// If the location doesn't exist or has permission issues, it will be ignored.
	parsedFile := o.renderSrc.GetManifestResources(deployInfo.ChartName, deployInfo.ChartPath)
	if parsedFile.IsResultConclusive() {
		return parsedFile.FilterOsErrors()
	}

	// 2. check cached manifest from previous processing
	// If the rendered manifest folder doesn't exist or has permission issues,
	// it will be ignored.
	parsedFile = o.renderSrc.GetCachedResources(deployInfo.ChartName, deployInfo.ChartPath)
	if parsedFile.IsResultConclusive() {
		return parsedFile.FilterOsErrors()
	}

	// 3. render new manifests
	// Depending upon the chart the request will be sent to a processor,
	// either Helm or Kustomize.
	parsedFile = o.renderSrc.GetRawManifest(deployInfo)
	// If there is any type of error return from here, as there is nothing to be cached.
	if parsedFile.GetRawError() != nil {
		// no manifest could be processed
		return parsedFile
	}

	// 4. persist static charts
	// if deployInfo.ChartPath is not passed, it means that the chart is not static
	if deployInfo.ChartPath == "" {
		return parsedFile
	}
	// Write rendered manifest static chart to deployInfo.ChartPath.
	// If the location doesn't exist or has permission issues, it will be ignored.
	err := util.WriteToFile(util.GetFsManifestChartPath(deployInfo.ChartPath), []byte(parsedFile.GetContent()))
	return types.NewParsedFile(parsedFile.GetContent(), err).FilterOsErrors()
}
