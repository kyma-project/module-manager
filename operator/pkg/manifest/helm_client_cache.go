package manifest

import (
	"sync"

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
	cache sync.Map
}

// NewHelmClientCache returns a new instance of RemoteClusterCache.
func NewHelmClientCache() *HelmClientCache {
	return &HelmClientCache{
		cache: sync.Map{},
	}
}

// Get loads the HelmClient from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) Get(key client.ObjectKey) types.HelmClient {
	value, ok := h.cache.Load(key)
	if !ok {
		return nil
	}
	return value.(types.HelmClient)
}

// Set saves the passed HelmClient into HelmClientCache for the client.ObjectKey.
func (h *HelmClientCache) Set(key client.ObjectKey, helmClient types.HelmClient) {
	h.cache.Store(key, helmClient)
}

// Delete deletes the HelmClient from HelmClientCache for the passed client.ObjectKey.
func (h *HelmClientCache) Delete(key client.ObjectKey) {
	h.cache.Delete(key)
}
