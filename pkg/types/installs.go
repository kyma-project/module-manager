package types

import (
	"helm.sh/helm/v3/pkg/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RefTypeMetadata specifies the type of installation specification
// that could be provided as part of a custom resource.
// This time is used in codec to successfully decode from raw extensions.
// +kubebuilder:validation:Enum=helm-chart;oci-ref;"kustomize";""
type RefTypeMetadata string

func (r RefTypeMetadata) NotEmpty() bool {
	return r != NilRefType
}

const (
	HelmChartType RefTypeMetadata = "helm-chart"
	OciRefType    RefTypeMetadata = "oci-ref"
	KustomizeType RefTypeMetadata = "kustomize"
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

// +k8s:deepcopy-gen=true
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
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`

	// CredSecretSelector is on optional field, for OCI image saved in private registry,
	// use it to indicate the secret which contains registry credentials,
	// must exist in the namespace same as manifest
	// +kubebuilder:validation:Optional
	CredSecretSelector *metav1.LabelSelector `json:"credSecretSelector,omitempty"`
}

// +k8s:deepcopy-gen=true
// HelmChartSpec defines the specification for a helm chart.
type HelmChartSpec struct {
	// URL defines the helm repo URL
	// +kubebuilder:validation:Optional
	URL string `json:"url"`

	// ChartName defines the helm chart name
	// +kubebuilder:validation:Optional
	ChartName string `json:"chartName"`

	// Type defines the chart as "helm-chart"
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`
}

// KustomizeSpec defines the specification for a Kustomize specification.
type KustomizeSpec struct {
	// Path defines the Kustomize local path
	Path string `json:"path"`

	// URL defines the Kustomize remote URL
	URL string `json:"url"`

	// Type defines the chart as "kustomize"
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`
}

// ManifestResources holds a collection of objects, so that we can filter / sequence them.
type ManifestResources struct {
	Items []*unstructured.Unstructured
	Blobs [][]byte
}

// ClusterInfo describes client and config for a cluster.
type ClusterInfo struct {
	Config *rest.Config
	Client client.Client
}

// IsEmpty indicates if ClusterInfo is empty.
func (r ClusterInfo) IsEmpty() bool {
	return r.Config == nil
}

type ResourceLists struct {
	Target    kube.ResourceList
	Installed kube.ResourceList
	Namespace kube.ResourceList
}

// ChartInfo defines helm chart information.
type ChartInfo struct {
	ChartPath   string
	RepoName    string
	URL         string
	ChartName   string
	ReleaseName string
}
