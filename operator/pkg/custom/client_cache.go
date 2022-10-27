package custom

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// RemoteClusterCache provides an in-memory cache to cache rest config and client instances
// for remote clusters. By using sync.Map for caching,
// concurrent operations to the cache from diverse reconciliations are considered safe.
//
// Inside the cache component owner name (in Client.ObjectKey format)
// is used as key and custom.ClusterInfo is stored as the value.
type RemoteClusterCache struct {
	cache sync.Map
}

// NewRemoteClusterCache returns a new instance of RemoteClusterCache.
func NewRemoteClusterCache() *RemoteClusterCache {
	return &RemoteClusterCache{
		cache: sync.Map{},
	}
}

// Get loads ClusterInfo from RemoteClusterCache for the passed client.ObjectKey.
func (r *RemoteClusterCache) Get(key client.ObjectKey) types.ClusterInfo {
	value, ok := r.cache.Load(key)
	if !ok {
		return types.ClusterInfo{}
	}
	return value.(types.ClusterInfo)
}

// Set saves the passed ClusterInfo into RemoteClusterCache for the client.ObjectKey.
func (r *RemoteClusterCache) Set(key client.ObjectKey, info types.ClusterInfo) {
	r.cache.Store(key, info)
}

// Delete deletes ClusterInfo from RemoteClusterCache for the passed client.ObjectKey.
func (r *RemoteClusterCache) Delete(key client.ObjectKey) {
	r.cache.Delete(key)
}
