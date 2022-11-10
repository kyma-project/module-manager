package types

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RendererCache offers utility methods to access RenderSrc cached instances.
type RendererCache interface {
	GetProcessor(key client.ObjectKey) RenderSrc
	SetProcessor(key client.ObjectKey, renderSrc RenderSrc)
	DeleteProcessor(key client.ObjectKey)
	GetConfig(key client.ObjectKey) string
	SetConfig(key client.ObjectKey, cfg string)
	DeleteConfig(key client.ObjectKey)
}

// ClusterInfoCache offers utility methods to access ClusterInfo cached instances.
type ClusterInfoCache interface {
	Get(key client.ObjectKey) ClusterInfo
	Set(key client.ObjectKey, info ClusterInfo)
	Delete(key client.ObjectKey)
}

// ManifestConfig offers utility methods to access resource relevant config.
type ManifestConfig interface {
	Get(key client.ObjectKey) ClusterInfo
	Set(key client.ObjectKey, cfg any)
	Delete(key client.ObjectKey)
}
