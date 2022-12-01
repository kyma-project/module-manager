package manifest

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/cli"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	manifestClient "github.com/kyma-project/module-manager/pkg/client"

	"github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/resource"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"k8s.io/apimachinery/pkg/api/meta"
)

type Operations struct {
	logger             logr.Logger
	renderSrc          types.ManifestClient
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
	client             client.Client
}

var (
	ErrCRsNotRemoved  = errors.New("CustomResources not completely removed")
	ErrCRDsNotRemoved = errors.New("CRDs not completely removed")
)

// InstallChart installs the resources based on types.InstallInfo and an appropriate rendering mechanism.
func InstallChart(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.install(deployInfo)
}

// UninstallChart uninstalls the resources based on types.InstallInfo and an appropriate rendering mechanism.
func UninstallChart(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.uninstall(deployInfo)
}

// ConsistencyCheck verifies consistency of resources based on types.InstallInfo and an appropriate rendering mechanism.
func ConsistencyCheck(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (bool, error) {
	ops, err := NewOperations(logger, deployInfo, resourceTransforms, cache)
	if err != nil {
		return false, err
	}

	return ops.consistencyCheck(deployInfo)
}

func NewOperations(logger logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
	cache types.RendererCache,
) (*Operations, error) {
	renderSrc, err := getRenderSrc(cache, deployInfo, logger)
	if err != nil {
		return nil, fmt.Errorf("unable to create manifest processor: %w", err)
	}
	clusterInfo, err := renderSrc.GetClusterInfo()
	if err != nil {
		return nil, err
	}

	ops := &Operations{
		logger:             logger,
		renderSrc:          renderSrc,
		flags:              deployInfo.Flags,
		resourceTransforms: resourceTransforms,
		client:             clusterInfo.Client,
	}

	return ops, nil
}

// getRenderSrc checks if the manifest processor client is cached and returns if available.
// If not available, it creates a new one based on deployInfo.
// Additionally, it verifies cached configuration for the manifest processor and invalidates it if required.
func getRenderSrc(cache types.RendererCache, deployInfo types.InstallInfo,
	logger logr.Logger,
) (types.ManifestClient, error) {
	var renderSrc types.ManifestClient

	/* Manifest processor handling */
	clusterCacheKey, err := discoverCacheKey(deployInfo.BaseResource, logger)
	if err != nil {
		return nil, err
	}

	if cache == nil {
		// cache disabled
		// create a new manifest processor on each call
		return getManifestProcessor(deployInfo, logger)
	}

	// look for existing processor entries
	// read manifest renderer from processor
	if renderSrc = cache.GetProcessor(clusterCacheKey); renderSrc == nil {
		renderSrc, err = getManifestProcessor(deployInfo, logger)
		if err != nil {
			return nil, err
		}
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
	if configHash == 0 {
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
func discoverCacheKey(resource client.Object, logger logr.Logger) (client.ObjectKey, error) {
	if resource == nil {
		return client.ObjectKey{}, errors.New("cannot discover cache-key based on empty resource")
	}

	label, err := util.GetResourceLabel(resource, labels.CacheKey)
	objectKey := client.ObjectKeyFromObject(resource)
	var labelErr *types.LabelNotFoundError
	if errors.As(err, &labelErr) {
		logger.V(util.DebugLogLevel).Info(labels.CacheKey+" missing on resource, it will be cached "+
			"based on resource name and namespace.",
			"resource", objectKey)
		return objectKey, nil
	}

	logger.V(util.DebugLogLevel).Info("resource will be cached based on "+labels.CacheKey,
		"resource", objectKey,
		"label", labels.CacheKey,
		"labelValue", label)

	return client.ObjectKey{Name: label, Namespace: resource.GetNamespace()}, nil
}

// getManifestProcessor returns a new types.ManifestClient instance
// this render source will handle subsequent Operations for manifest resources based on types.InstallInfo.
func getManifestProcessor(deployInfo types.InstallInfo, logger logr.Logger) (types.ManifestClient, error) {
	render := NewRendered(logger)

	singletonClients, err := manifestClient.NewSingletonClients(deployInfo.ClusterInfo, logger)
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
			render, deployInfo, true)
	case resource.KustomizeKind:
		// create dynamic client for rest config
		if err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %w", err)
		}
		return NewKustomizeProcessor(singletonClients, logger, render)
	}
	return nil, nil
}

func (o *Operations) consistencyCheck(deployInfo types.InstallInfo) (bool, error) {
	// verify CRDs
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, o.client,
		false); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// verify CR
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, o.client,
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

func (o *Operations) install(deployInfo types.InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, o.client, true); err != nil {
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
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, o.client,
		true); err != nil {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *Operations) uninstall(deployInfo types.InstallInfo) (bool, error) {
	// delete crs first - proceed only if not found
	// proceed if CR type doesn't exist anymore - since associated CRDs might be deleted from resource uninstallation
	// since there might be a deletion process to be completed by other manifest resources
	crDeleted := resource.RemoveCRs(deployInfo.Ctx, deployInfo.CustomResources, o.client)
	if !crDeleted {
		return false, ErrCRsNotRemoved
	}

	// process manifest
	parsedFile := o.getManifestForChartPath(deployInfo)
	if parsedFile.GetRawError() != nil {
		return false, parsedFile.GetRawError()
	}
	// remove cached manifest
	if parsedFile := o.renderSrc.DeleteCachedResources(deployInfo.ChartPath); parsedFile.GetRawError() != nil {
		return false, parsedFile.GetRawError()
	}

	// uninstall resources
	consistent, err := o.renderSrc.Uninstall(parsedFile.GetContent(), deployInfo, o.resourceTransforms)
	if !noResourceFound(err) || !consistent {
		return false, err
	}

	// delete crds last - if not present ignore!
	crdDeleted := resource.RemoveCRDs(deployInfo.Ctx, deployInfo.Crds, o.client)
	if !crdDeleted {
		return false, ErrCRDsNotRemoved
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, err
}

func noResourceFound(err error) bool {
	return err == nil || apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

func (o *Operations) getManifestForChartPath(installInfo types.InstallInfo) *types.ParsedFile {
	// 1. check provided manifest file
	// It is expected for installInfo.ChartPath to contain ONE .yaml or .yml file,
	// which is assumed to contain a list of resources to be processed.
	// If the location doesn't exist or has permission issues, it will be ignored.
	parsedFile := o.renderSrc.GetManifestResources(installInfo.ChartPath)
	if parsedFile.IsResultConclusive() {
		o.logger.V(util.DebugLogLevel).Info("resolved manifest from chart-path")
		return parsedFile.FilterOsErrors()
	}

	// 2. check cached manifest from previous processing
	// If the rendered manifest folder doesn't exist or has permission issues,
	// it will be ignored.
	parsedFile = o.renderSrc.GetCachedResources(installInfo.ChartName, installInfo.ChartPath)
	if parsedFile.IsResultConclusive() {
		o.logger.V(util.DebugLogLevel).Info("resolved manifest (cached from a previous render) from chart-path")
		return parsedFile.FilterOsErrors()
	}

	// 3. render new manifests
	// Depending upon the chart the request will be sent to a processor,
	// either Helm or Kustomize.
	parsedFile = o.renderSrc.GetRawManifest(installInfo)
	// If there is any type of error return from here, as there is nothing to be cached.
	if parsedFile.GetRawError() != nil {
		// no manifest could be processed
		return parsedFile
	}
	o.logger.V(util.DebugLogLevel).Info("rendered manifest from chart-path")

	// 4. persist static charts
	// if installInfo.ChartPath is not passed, it means that the chart is not static
	if installInfo.ChartPath == "" {
		return parsedFile
	}
	// Write Rendered manifest static chart to installInfo.ChartPath.
	// If the location doesn't exist or has permission issues, it will be ignored.
	err := util.WriteToFile(util.GetFsManifestChartPath(installInfo.ChartPath), []byte(parsedFile.GetContent()))
	return types.NewParsedFile(parsedFile.GetContent(), err).FilterOsErrors()
}
