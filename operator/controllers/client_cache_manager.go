package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type CacheManager struct {
	RenderSources types.RendererCache
	ClusterInfos  types.ClusterInfoCache
}

func NewCacheManager() *CacheManager {
	return &CacheManager{
		RenderSources: manifest.NewRendererCache(),
		ClusterInfos:  custom.NewRemoteClusterCache(),
	}
}

func (c *CacheManager) Invalidate(key client.ObjectKey) {
	c.RenderSources.DeleteProcessor(key)
	c.ClusterInfos.Delete(key)
}
