package types

import (
	"context"

	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RenderSrc interface {
	GetRawManifest(deployInfo InstallInfo) *ParsedFile
	Install(manifest string, deployInfo InstallInfo, transforms []ObjectTransform) (bool, error)
	Uninstall(manifest string, deployInfo InstallInfo, transforms []ObjectTransform) (bool, error)
	IsConsistent(manifest string, deployInfo InstallInfo, transforms []ObjectTransform) (bool, error)
	Transform(ctx context.Context, manifest string, base BaseCustomObject,
		transforms []ObjectTransform) (*ManifestResources, error)
	GetCachedResources(chartName, chartPath string) *ParsedFile
	DeleteCachedResources(chartPath string) *ParsedFile
	GetManifestResources(chartName, dirPath string) *ParsedFile
	InvalidateConfigAndRenderedManifest(deployInfo InstallInfo, cachedHash string) (string, error)
}

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
}

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

func (r *ResourceLists) GetWaitForResources() kube.ResourceList {
	return append(r.Target, r.Namespace...)
}

func (r *ResourceLists) GetResourcesToBeDeleted() kube.ResourceList {
	return append(r.Installed, r.Namespace...)
}

// InstallInfo represents deployment information artifacts to be processed.
type InstallInfo struct {
	// ChartInfo represents chart information to be processed
	*ChartInfo
	// ResourceInfo represents additional resources to be processed
	ResourceInfo
	// ClusterInfo represents target cluster information
	ClusterInfo
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

type ResponseChan chan *InstallResponse

//nolint:errname
type InstallResponse struct {
	Ready             bool
	ChartName         string
	Flags             ChartFlags
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *InstallResponse) Error() string {
	return r.Err.Error()
}

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)
