package types

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
)

type Applier interface {
	Apply(deployInfo InstallInfo, objects *ManifestResources, namespace string) (bool, error)
}

type ApplierOptions struct {
	Manifest string

	RESTConfig *rest.Config
	RESTMapper meta.RESTMapper
	Namespace  string
	Validate   bool
	ExtraArgs  []string
}
