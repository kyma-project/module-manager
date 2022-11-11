package manifest

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/cli"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

// ResourceInfo represents additional resources.
type ResourceInfo struct {
	// BaseResource represents base custom resource that is being reconciled
	BaseResource *unstructured.Unstructured
	// CustomResources represents a set of additional custom resources to be installed
	CustomResources []*unstructured.Unstructured
	// Crds represents a set of additional custom resource definitions to be installed
	Crds []*apiextensions.CustomResourceDefinition
}

type operations struct {
	logger             logr.Logger
	renderSrc          types.RenderSrc
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
}

// InstallChart installs the resources based on types.InstallInfo and an appropriate rendering mechanism.
func InstallChart(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.install(deployInfo)
}

// UninstallChart uninstalls the resources based on types.InstallInfo and an appropriate rendering mechanism.
func UninstallChart(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.uninstall(deployInfo)
}

// ConsistencyCheck verifies consistency of resources based on types.InstallInfo and an appropriate rendering mechanism.
func ConsistencyCheck(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := newOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.consistencyCheck(deployInfo)
}

func newOperations(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (*operations, error) {
	cacheKey := discoverCacheKey(deployInfo.BaseResource, logger)

	var renderSrc types.RenderSrc
	if cache != nil && cacheKey.Name != "" {
		// read manifest renderer from cache
		renderSrc = cache.Get(cacheKey)
	}
	// cache entry not found
	if renderSrc == nil {
		var err error
		render := NewRendered(logger)
		txformer := NewTransformer()
		renderSrc, err = getManifestProcessor(deployInfo, logger, render, txformer)
		if err != nil {
			return nil, fmt.Errorf("unable to create manifest processor: %w", err)
		}
		if cache != nil && cacheKey.Name != "" {
			// cache manifest renderer
			cache.Set(cacheKey, renderSrc)
		}
	}

	ops := &operations{
		logger:             logger,
		renderSrc:          renderSrc,
		flags:              deployInfo.Flags,
		resourceTransforms: resourceTransforms,
	}

	return ops, nil
}

// discoverCacheKey returns cache key for caching of manifest renderer,
// by label value operator.kyma-project.io/cache-key.
// If label not found on base resource an empty cache key is returned.
func discoverCacheKey(resource client.Object, logger logr.Logger) client.ObjectKey {
	if resource != nil {
		label, err := util.GetResourceLabel(resource, labels.CacheKey)
		var labelErr *util.LabelNotFoundError
		if errors.As(err, &labelErr) {
			logger.V(util.DebugLogLevel).Info("cache-key label missing, resource will not be cached. Resulted in",
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
func getManifestProcessor(deployInfo types.InstallInfo,
	logger logr.Logger, render *rendered, txformer *transformer,
) (types.RenderSrc, error) {
	singletonClients, err := NewSingletonClients(deployInfo.Config, logger)
	if err != nil {
		return nil, err
	}

	chartKind, err := resource.GetChartKind(deployInfo)
	if err != nil {
		return nil, err
	}
	switch chartKind {
	case resource.HelmKind, resource.UnknownKind:
		// create HelmClient instance
		return NewHelmProcessor(singletonClients, cli.New(), logger,
			render, txformer)
	case resource.KustomizeKind:
		// create dynamic client for rest config
		if err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %w", err)
		}
		return NewKustomizeProcessor(singletonClients, logger, render, txformer)
	}
	return nil, nil
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
	consistent, err := o.renderSrc.Install(parsedFile.GetContent(), deployInfo, o.resourceTransforms)
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
