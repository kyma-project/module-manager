package types

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HelmClientCache offers utility methods to access HelmClient cached instances.
type HelmClientCache interface {
	Get(key client.ObjectKey) HelmClient
	Set(key client.ObjectKey, helmClient HelmClient)
	Delete(key client.ObjectKey)
}

// ClusterInfoCache offers utility methods to access ClusterInfo cached instances.
type ClusterInfoCache interface {
	Get(key client.ObjectKey) ClusterInfo
	Set(key client.ObjectKey, info ClusterInfo)
	Delete(key client.ObjectKey)
}
