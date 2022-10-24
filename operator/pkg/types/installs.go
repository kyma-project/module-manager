package types

import (
	"context"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

type HelmClient interface {
	NewInstallActionClient(namespace, releaseName string, flags ChartFlags,
	) (*action.Install, error)
	SetFlags(flags ChartFlags, actionClient *action.Install) error
	DownloadChart(actionClient *action.Install, chartName string) (string, error)
	GetNsResource(actionClient *action.Install, operationType HelmOperation,
	) (kube.ResourceList, error)
	GetTargetResources(ctx context.Context, manifest string, targetNamespace string,
		transforms []ObjectTransform, object BaseCustomObject,
	) (kube.ResourceList, error)
	PerformUpdate(resourceLists ResourceLists, force bool,
	) (*kube.Result, error)
	PerformCreate(resourceLists ResourceLists) (*kube.Result, error)
	PerformDelete(resourceLists ResourceLists) (int, error)
	CheckWaitForResources(targetResources kube.ResourceList, actionClient *action.Install,
		operation HelmOperation,
	) error
	CheckDesiredState(ctx context.Context, targetResources kube.ResourceList, operation HelmOperation,
	) (bool, error)
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

func (r ResourceLists) GetWaitForResources() kube.ResourceList {
	return append(r.Target, r.Namespace...)
}
