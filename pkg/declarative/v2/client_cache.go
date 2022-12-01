package v2

import (
	"sync"

	manifestClient "github.com/kyma-project/module-manager/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SingletonClientCache interface {
	GetClients(key any) *manifestClient.SingletonClients
	SetClients(key client.ObjectKey, clients *manifestClient.SingletonClients)
}

type MemorySingletonClientCache struct {
	cache sync.Map // Cluster specific
}

// NewMemorySingletonClientCache returns a new instance of MemorySingletonClientCache.
func NewMemorySingletonClientCache() *MemorySingletonClientCache {
	return &MemorySingletonClientCache{
		cache: sync.Map{},
	}
}

func (r *MemorySingletonClientCache) GetClients(key any) *manifestClient.SingletonClients {
	value, ok := r.cache.Load(key)
	if !ok {
		return nil
	}
	return value.(*manifestClient.SingletonClients)
}

func (r *MemorySingletonClientCache) SetClients(key client.ObjectKey, clients *manifestClient.SingletonClients) {
	r.cache.Store(key, clients)
}
