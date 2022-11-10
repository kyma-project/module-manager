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

func (mc *ManifestClientCache) GetRendererCache() types.RendererCache {
	return mc.renderSources
}

func (mc *ManifestClientCache) GetClusterInfoCache() types.ClusterInfoCache {
	return mc.clusterInfos
}

func (mc *ManifestClientCache) InvalidateForOwner(key client.ObjectKey) {
	mc.renderSources.DeleteProcessor(key)
	mc.clusterInfos.Delete(key)
}

func (mc *ManifestClientCache) InvalidateSelf(key client.ObjectKey) {
	mc.renderSources.DeleteConfig(key)
}
