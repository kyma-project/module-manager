package manifest

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// RendererCache provides an in-memory processor to store types.RenderSrc instances applicable
// to remote clusters. By using sync.Map for caching,
// concurrent operations to the processor from diverse reconciliations are considered safe.
//
// Inside the processor component owner name (in Client.ObjectKey format)
// is used as key and the types.RenderSrc instance is stored as the value.
type RendererCache struct {
	processor sync.Map
	config    sync.Map
}

// NewRendererCache returns a new instance of RemoteClusterCache.
func NewRendererCache() *RendererCache {
	return &RendererCache{
		processor: sync.Map{},
		config:    sync.Map{},
	}
}

// GetProcessor loads the types.RenderSrc from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) GetProcessor(key client.ObjectKey) types.RenderSrc {
	value, ok := r.processor.Load(key)
	if !ok {
		return nil
	}
	return value.(types.RenderSrc)
}

// SetProcessor saves the passed types.RenderSrc into RendererCache for the client.ObjectKey.
func (r *RendererCache) SetProcessor(key client.ObjectKey, helmClient types.RenderSrc) {
	r.processor.Store(key, helmClient)
}

// DeleteProcessor deletes the types.RenderSrc from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) DeleteProcessor(key client.ObjectKey) {
	r.processor.Delete(key)
}

// GetConfig loads the configuration from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) GetConfig(key client.ObjectKey) uint32 {
	value, ok := r.config.Load(key)
	if !ok {
		return 0
	}
	return value.(uint32)
}

// SetConfig saves the passed configuration into RendererCache for the client.ObjectKey.
func (r *RendererCache) SetConfig(key client.ObjectKey, config uint32) {
	r.config.Store(key, config)
}

// DeleteConfig deletes the configuration from RendererCache for the passed client.ObjectKey.
func (r *RendererCache) DeleteConfig(key client.ObjectKey) {
	r.config.Delete(key)
}
