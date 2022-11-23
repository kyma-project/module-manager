package cache

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/pkg/manifest"
	"github.com/kyma-project/module-manager/pkg/types"
)

type ManifestClientCache struct {
	renderSources types.RendererCache
}

func NewCacheManager() types.CacheManager {
	return &ManifestClientCache{
		renderSources: manifest.NewRendererCache(),
	}
}

// GetRendererCache returns renderer cache which contains entries for processor and configuration.
func (mc *ManifestClientCache) GetRendererCache() types.RendererCache {
	return mc.renderSources
}

// InvalidateForOwner invalidates entries cached by owner key.
// E.g. for Manifest CR a Kyma CR is an owner. So the cache entries for owner will be cached.
func (mc *ManifestClientCache) InvalidateForOwner(key client.ObjectKey) {
	mc.renderSources.DeleteProcessor(key)
}

// InvalidateSelf invalidates entries assigned by the resource being processed.
func (mc *ManifestClientCache) InvalidateSelf(key client.ObjectKey) {
	mc.renderSources.DeleteConfig(key)
}
