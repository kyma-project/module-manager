package custom

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RemoteClusterCache provides an in-memory cache to store rest config and client instances
// for remote clusters. By using sync.Map for caching,
// concurrent operations to the cache from diverse reconciliations are considered safe.
//
// Inside the cache client.ObjectKey type is used as key and custom.ClusterInfo is stored as the value.
type RemoteClusterCache struct {
	cache sync.Map
}

// NewRemoteClusterCache returns a new instance of RemoteClusterCache.
func NewRemoteClusterCache() *RemoteClusterCache {
	return &RemoteClusterCache{
		cache: sync.Map{},
	}
}

// Get loads the custom.ClusterInfo from cache for the passed client.ObjectKey.
func (r *RemoteClusterCache) Get(key client.ObjectKey) ClusterInfo {
	value, ok := r.cache.Load(key)
	if !ok {
		return ClusterInfo{}
	}
	return value.(ClusterInfo)
}

// Set saves the passed custom.ClusterInfo into cache for the client.ObjectKey.
func (r *RemoteClusterCache) Set(key client.ObjectKey, info ClusterInfo) {
	r.cache.Store(key, info)
}

// Delete deletes the custom.ClusterInfo from cache for the passed client.ObjectKey.
func (r *RemoteClusterCache) Delete(key client.ObjectKey) {
	r.cache.Delete(key)
}
