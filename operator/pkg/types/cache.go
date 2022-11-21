package types

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CacheManager contains ClusterInfoCache and RendererCache cached entries.
// It offers utility methods to access the underlying caches as well as invalidate entries.
type CacheManager interface {
	// InvalidateForOwner invalidates all cache entries which are cached by an owner key.
	// Here all caches should be deleted where entries are unique by a parent resource.
	InvalidateForOwner(key client.ObjectKey)

	// InvalidateSelf invalidates all cache entries which are cached on resource level.
	// Here all cache entries should be deleted where entries are unique by a resource itself.
	InvalidateSelf(key client.ObjectKey)

	// GetRendererCache returns the cache stored under RendererCache.
	GetRendererCache() RendererCache
}

// RendererCache offers utility methods to access ManifestClient cached instances.
type RendererCache interface {
	// GetProcessor returns the manifest processor cached entry by key passed.
	GetProcessor(key client.ObjectKey) ManifestClient

	// SetProcessor sets the manifest processor cached entry by key passed.
	SetProcessor(key client.ObjectKey, renderSrc ManifestClient)

	// DeleteProcessor deletes the manifest processor cached entry by key passed.
	DeleteProcessor(key client.ObjectKey)

	// GetConfig returns the hashed configuration cached entry by key passed.
	GetConfig(key client.ObjectKey) uint32

	// SetConfig returns the hashed configuration cached entry by key passed.
	SetConfig(key client.ObjectKey, cfg uint32)

	// DeleteConfig deletes the hashed configuration cached entry by key passed.
	DeleteConfig(key client.ObjectKey)
}

// ClusterInfoCache offers utility methods to access ClusterInfo cached instances.
type ClusterInfoCache interface {
	// Get returns the cluster information cached entry by the key passed.
	Get(key client.ObjectKey) ClusterInfo

	// Set sets the cluster information cached entry by the key passed.
	Set(key client.ObjectKey, info ClusterInfo)

	// Delete deletes the cluster information cached entry by the key passed.
	Delete(key client.ObjectKey)
}
