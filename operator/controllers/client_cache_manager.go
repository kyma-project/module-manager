package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type CacheManager struct {
	HelmClients  types.HelmClientCache
	ClusterInfos types.ClusterInfoCache
}

func NewCacheManager() *CacheManager {
	return &CacheManager{
		HelmClients:  manifest.NewHelmClientCache(),
		ClusterInfos: custom.NewRemoteClusterCache(),
	}
}

func (c *CacheManager) Invalidate(key client.ObjectKey) {
	c.HelmClients.Delete(key)
	c.HelmClients.DeleteMemCachedClient(key)
	c.ClusterInfos.Delete(key)
}
