package types

import (
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type HelmClientCache interface {
	Get(key client.ObjectKey) HelmClient
	Set(key client.ObjectKey, helmClient HelmClient)
	Delete(key client.ObjectKey)
	GetMemCachedClient(key client.ObjectKey) discovery.CachedDiscoveryInterface
	SetMemCachedClient(key client.ObjectKey, cachedClient discovery.CachedDiscoveryInterface)
	DeleteMemCachedClient(key client.ObjectKey)
}

type ClusterInfoCache interface {
	Get(key client.ObjectKey) ClusterInfo
	Set(key client.ObjectKey, info ClusterInfo)
	Delete(key client.ObjectKey)
}
