package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type ManifestClientCache struct {
	renderSources types.RendererCache
	clusterInfos  types.ClusterInfoCache
}

func NewCacheManager() types.CacheManager {
	return &ManifestClientCache{
		renderSources: manifest.NewRendererCache(),
		clusterInfos:  custom.NewRemoteClusterCache(),
	}
}

// GetRendererCache returns renderer cache which contains entries for processor and configuration.
func (mc *ManifestClientCache) GetRendererCache() types.RendererCache {
	return mc.renderSources
}

// GetClusterInfoCache returns cluster info cache containing entries for target cluster information.
func (mc *ManifestClientCache) GetClusterInfoCache() types.ClusterInfoCache {
	return mc.clusterInfos
}

// InvalidateForOwner invalidates entries cached by owner key.
// E.g. for Manifest CR a Kyma CR is an owner. So the cache entries for owner will be cached.
func (mc *ManifestClientCache) InvalidateForOwner(key client.ObjectKey) {
	mc.renderSources.DeleteProcessor(key)
	mc.clusterInfos.Delete(key)
}

// InvalidateSelf invalidates entries assigned by the resource being processed.
func (mc *ManifestClientCache) InvalidateSelf(key client.ObjectKey) {
	mc.renderSources.DeleteConfig(key)
}
