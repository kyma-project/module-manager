package types

import (
	"context"

	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ManifestClient offers client utility methods for processing of manifest resources.
type ManifestClient interface {
	// GetRawManifest returns processed resource manifest using the client.
	GetRawManifest(deployInfo *InstallInfo) *ParsedFile

	// Install transforms and applies resources based on InstallInfo.
	Install(manifest string, deployInfo *InstallInfo, transforms []ObjectTransform, postRuns []PostRun) (bool, error)

	// Uninstall transforms and applies resources based on InstallInfo.
	Uninstall(manifest string, deployInfo *InstallInfo, transforms []ObjectTransform, postRuns []PostRun) (bool, error)

	// IsConsistent transforms and checks if resources are consistently installed based on InstallInfo.
	IsConsistent(manifest string, deployInfo *InstallInfo, transforms []ObjectTransform, postRuns []PostRun) (bool, error)

	// GetCachedResources returns a resource manifest which was already cached during previous operations.
	// By default, it looks inside <chart-path>/manifest/manifest.yaml
	GetCachedResources(chartName, chartPath string) *ParsedFile

	// DeleteCachedResources deletes cached resource manifest.
	// By default, it deletes <chart-path>/manifest/manifest.yaml.
	DeleteCachedResources(chartPath string) *ParsedFile

	// GetManifestResources returns a pre-rendered resource manifest file located at the passed chartPath.
	// If multiple YAML files are present at the dirPath, it cannot resolve the file to be used, hence will not process.
	GetManifestResources(dirPath string) *ParsedFile

	// InvalidateConfigAndRenderedManifest compares the cached hash with the processed hash for helm flags.
	// On mismatch, it invalidates the cached manifest and resets flags on client.
	InvalidateConfigAndRenderedManifest(deployInfo *InstallInfo, cachedHash uint32) (uint32, error)

	// GetClusterInfo returns Client and REST config for the target cluster
	GetClusterInfo() (ClusterInfo, error)
}

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

type OperationType string

type HelmOperation OperationType

const (
	OperationCreate HelmOperation = "create"
	OperationDelete HelmOperation = "delete"
)

type ResourceLists struct {
	Target    kube.ResourceList
	Installed kube.ResourceList
	Namespace kube.ResourceList
}

// InstallInfo represents deployment information artifacts to be processed.
type InstallInfo struct {
	// ChartInfo represents chart information to be processed
	*ChartInfo
	// ResourceInfo represents additional resources to be processed
	*ResourceInfo
	// ClusterInfo represents target cluster information
	*ClusterInfo
	// Ctx hold the current context
	Ctx context.Context //nolint:containedctx
	// CheckFn returns a boolean indicating ready state based on custom checks
	CheckFn CheckFnType
	// CheckReadyStates indicates if native resources should be checked for ready states
	CheckReadyStates bool
	// UpdateRepositories indicates if repositories should be updated
	UpdateRepositories bool
}

// ChartInfo defines helm chart information.
type ChartInfo struct {
	ChartPath   string
	RepoName    string
	URL         string
	ChartName   string
	ReleaseName string
	Flags       ChartFlags
}

// ResourceInfo represents additional resources.
type ResourceInfo struct {
	// BaseResource represents base custom resource that is being reconciled
	BaseResource *unstructured.Unstructured
	// CustomResources represents a set of additional custom resources to be installed
	CustomResources []*unstructured.Unstructured
	// Crds represents a set of additional custom resource definitions to be installed
	Crds []*v1.CustomResourceDefinition
}
