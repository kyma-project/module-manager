package manifest

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// RendererCacheImpl provides an in-memory processor to store types.ManifestClient instances applicable
// to remote clusters. By using sync.Map for caching,
// concurrent operations to the processor from diverse reconciliations are considered safe.
//
// Inside the processor cluster specific unique name (in Client.ObjectKey format)
// is used as key and the types.ManifestClient instance is stored as the value.
// Inside the config resource level configuration is stored by its key.
type RendererCacheImpl struct {
	processor sync.Map // Cluster specific
	config    sync.Map // Resource specific
}

// NewRendererCache returns a new instance of RemoteClusterCache.
func NewRendererCache() *RendererCacheImpl {
	return &RendererCacheImpl{
		processor: sync.Map{},
		config:    sync.Map{},
	}
}

// GetProcessor loads the types.ManifestClient from RendererCacheImpl for the passed client.ObjectKey.
func (r *RendererCacheImpl) GetProcessor(key client.ObjectKey) types.ManifestClient {
	value, ok := r.processor.Load(key)
	if !ok {
		return nil
	}
	return value.(types.ManifestClient)
}

// SetProcessor saves the passed types.ManifestClient into RendererCacheImpl for the client.ObjectKey.
func (r *RendererCacheImpl) SetProcessor(key client.ObjectKey, helmClient types.ManifestClient) {
	r.processor.Store(key, helmClient)
}

// DeleteProcessor deletes the types.ManifestClient from RendererCacheImpl for the passed client.ObjectKey.
func (r *RendererCacheImpl) DeleteProcessor(key client.ObjectKey) {
	r.processor.Delete(key)
}

// GetConfig loads the configuration from RendererCacheImpl for the passed client.ObjectKey.
func (r *RendererCacheImpl) GetConfig(key client.ObjectKey) uint32 {
	value, ok := r.config.Load(key)
	if !ok {
		return 0
	}
	return value.(uint32)
}

// SetConfig saves the passed configuration into RendererCacheImpl for the client.ObjectKey.
func (r *RendererCacheImpl) SetConfig(key client.ObjectKey, config uint32) {
	r.config.Store(key, config)
}

// DeleteConfig deletes the configuration from RendererCacheImpl for the passed client.ObjectKey.
func (r *RendererCacheImpl) DeleteConfig(key client.ObjectKey) {
	r.config.Delete(key)
}
