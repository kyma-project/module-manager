package manifest

import (
	"sync"

	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// HelmClientCache provides an in-memory cache to cache HelmClient instances applicable
// to remote clusters. By using sync.Map for caching,
// concurrent operations to the cache from diverse reconciliations are considered safe.
//
// Inside the cache component owner name (in Client.ObjectKey format)
// is used as key and HelmClient is stored as the value.
type HelmClientCache struct {
	clientCache sync.Map
	memCache    sync.Map
}

// NewHelmClientCache returns a new instance of RemoteClusterCache.
func NewHelmClientCache() *HelmClientCache {
	return &HelmClientCache{
		clientCache: sync.Map{},
		memCache:    sync.Map{},
	}
}

// Get loads the HelmClient from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) Get(key client.ObjectKey) types.HelmClient {
	value, ok := h.clientCache.Load(key)
	if !ok {
		return nil
	}
	return value.(types.HelmClient)
}

// Set saves the passed HelmClient into HelmClientCache for the client.ObjectKey.
func (h *HelmClientCache) Set(key client.ObjectKey, helmClient types.HelmClient) {
	h.clientCache.Store(key, helmClient)
}

// Delete deletes the HelmClient from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) Delete(key client.ObjectKey) {
	h.clientCache.Delete(key)
}

// GetMemCachedClient loads the CachedDiscoveryInterface from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) GetMemCachedClient(key client.ObjectKey) discovery.CachedDiscoveryInterface {
	value, ok := h.memCache.Load(key)
	if !ok {
		return nil
	}
	return value.(discovery.CachedDiscoveryInterface)
}

// SetMemCachedClient saves the passed CachedDiscoveryInterface into HelmClientCache for the client.ObjectKey.
func (h *HelmClientCache) SetMemCachedClient(key client.ObjectKey, cachedClient discovery.CachedDiscoveryInterface) {
	h.memCache.Store(key, cachedClient)
}

// DeleteMemCachedClient deletes the CachedDiscoveryInterface from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) DeleteMemCachedClient(key client.ObjectKey) {
	h.memCache.Delete(key)
}
