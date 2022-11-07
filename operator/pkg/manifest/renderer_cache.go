package manifest

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// RendererCache provides an in-memory cache to store types.RenderSrc instances applicable
// to remote clusters. By using sync.Map for caching,
// concurrent operations to the cache from diverse reconciliations are considered safe.
//
// Inside the cache component owner name (in Client.ObjectKey format)
// is used as key and the types.RenderSrc instance is stored as the value.
type RendererCache struct {
	cache sync.Map
}

// NewRendererCache returns a new instance of RemoteClusterCache.
func NewRendererCache() *RendererCache {
	return &RendererCache{
		cache: sync.Map{},
	}
}

// Get loads the types.RenderSrc from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) Get(key client.ObjectKey) types.RenderSrc {
	value, ok := r.cache.Load(key)
	if !ok {
		return nil
	}
	return value.(types.RenderSrc)
}

// Set saves the passed types.RenderSrc into RendererCache for the client.ObjectKey.
func (r *RendererCache) Set(key client.ObjectKey, helmClient types.RenderSrc) {
	r.cache.Store(key, helmClient)
}

// Delete deletes the types.RenderSrc from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) Delete(key client.ObjectKey) {
	r.cache.Delete(key)
}
