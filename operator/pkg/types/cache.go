package types

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RendererCache offers utility methods to access RenderSrc cached instances.
type RendererCache interface {
	Get(key client.ObjectKey) RenderSrc
	Set(key client.ObjectKey, renderSrc RenderSrc)
	Delete(key client.ObjectKey)
}

// ClusterInfoCache offers utility methods to access ClusterInfo cached instances.
type ClusterInfoCache interface {
	Get(key client.ObjectKey) ClusterInfo
	Set(key client.ObjectKey, info ClusterInfo)
	Delete(key client.ObjectKey)
}
