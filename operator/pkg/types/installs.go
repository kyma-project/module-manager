package types

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type RefTypeMetadata string

func (r RefTypeMetadata) NotEmpty() bool {
	return r != NilRefType
}

const (
	HelmChartType RefTypeMetadata = "helm-chart"
	OciRefType    RefTypeMetadata = "oci-ref"
	NilRefType    RefTypeMetadata = ""
)

// Flags define a set of configurable flags.
type Flags map[string]interface{}

// ChartFlags define flag based configurations for helm chart processing.
type ChartFlags struct {
	// ConfigFlags support string, bool and int types as helm chart flags
	// check: https://github.com/helm/helm/blob/d7b4c38c42cb0b77f1bcebf9bb4ae7695a10da0b/pkg/action/install.go#L67
	ConfigFlags Flags

	// SetFlags are chart value overrides
	SetFlags Flags
}

// ImageSpec defines OCI Image specifications.
type ImageSpec struct {
	// Repo defines the Image repo
	// +kubebuilder:validation:Optional
	Repo string `json:"repo"`

	// Name defines the Image name
	// +kubebuilder:validation:Optional
	Name string `json:"name"`

	// Ref is either a sha value, tag or version
	// +kubebuilder:validation:Optional
	Ref string `json:"ref"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref;""
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`
}

// HelmChartSpec defines the specification for a helm chart.
type HelmChartSpec struct {
	// URL defines the helm repo URL
	// +kubebuilder:validation:Optional
	URL string `json:"url"`

	// ChartName defines the helm chart name
	// +kubebuilder:validation:Optional
	ChartName string `json:"chartName"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`
}

// Objects holds a collection of objects, so that we can filter / sequence them.
type ManifestResources struct {
	Items []*unstructured.Unstructured
	Blobs [][]byte
}
