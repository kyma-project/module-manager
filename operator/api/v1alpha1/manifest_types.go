/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const ManifestKind = "Manifest"

func (m *Manifest) SetObservedGeneration() *Manifest {
	m.Status.ObservedGeneration = m.Generation
	return m
}

type CustomState struct {
	// APIVersion defines api version of the custom resource
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind defines the kind of the custom resource
	Kind string `json:"kind,omitempty"`

	// Name defines the name of the custom resource
	Name string `json:"name,omitempty"`

	// Namespace defines the namespace of the custom resource
	Namespace string `json:"namespace,omitempty"`

	// Namespace defines the desired state of the custom resource
	State string `json:"state,omitempty"`
}

// InstallInfo defines installation information
type InstallInfo struct {
	// Source can either be described as ImageSpec or HelmChartSpec
	//+kubebuilder:pruning:PreserveUnknownFields
	Source runtime.RawExtension `json:"source"`

	// Name specifies a unique install name for Manifest
	Name string `json:"name"`
}

// ImageSpec defines OCI Image specifications
type ImageSpec struct {
	// Repo defines the Image repo
	Repo string `json:"repo,omitempty"`

	// Name defines the Image name
	Name string `json:"name,omitempty"`

	// Ref is either a sha value, tag or version
	Ref string `json:"ref,omitempty"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref
	Type RefTypeMetadata `json:"type"`
}

// HelmChartSpec defines the specification for a helm chart
type HelmChartSpec struct {
	// Url defines the helm repo URL
	Url string `json:"url,omitempty"`

	// ChartName defines the helm chart name
	ChartName string `json:"chartName,omitempty"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref
	Type RefTypeMetadata `json:"type"`
}

type RefTypeMetadata string

const (
	HelmChartType RefTypeMetadata = "helm-chart"
	OciRefType    RefTypeMetadata = "oci-ref"
)

// ManifestSpec defines the specification of Manifest
type ManifestSpec struct {
	// Config specifies OCI image configuration for Manifest
	// +kubebuilder:validation:Optional
	Config ImageSpec `json:"config,omitempty"`

	// Installs specifies a list of installations for Manifest
	Installs []InstallInfo `json:"installs,omitempty"`

	// CustomStates specifies a list of resources with their desires states for Manifest
	// +kubebuilder:validation:Optional
	CustomStates []CustomState `json:"customStates,omitempty"`

	//+kubebuilder:pruning:PreserveUnknownFields
	//+kubebuilder:validation:XEmbeddedResource
	//+kubebuilder:object:generate=false
	// CustomResource specifies a resource which will be watched for state updates
	CustomResource unstructured.Unstructured `json:"customResource,omitempty"`

	// CustomResourceDefinitions specifies the custom resource definitions' ImageSpec
	CustomResourceDefinitions []ImageSpec `json:"customResourcesDefinitions,omitempty"`
}

// +kubebuilder:validation:Enum=Processing;Deleting;Ready;Error
type ManifestState string

// Valid Helm States
const (
	// ManifestStateReady signifies Manifest is ready
	ManifestStateReady ManifestState = "Ready"

	// ManifestStateProcessing signifies Manifest is reconciling
	ManifestStateProcessing ManifestState = "Processing"

	// ManifestStateError signifies an error for Manifest
	ManifestStateError ManifestState = "Error"

	// ManifestStateDeleting signifies Manifest is being deleted
	ManifestStateDeleting ManifestState = "Deleting"
)

// ManifestStatus defines the observed state of Manifest
type ManifestStatus struct {
	State ManifestState `json:"state,omitempty"`

	// List of status conditions to indicate the status of Manifest.
	// +kubebuilder:validation:Optional
	Conditions []ManifestCondition `json:"conditions,omitempty"`

	// Observed generation
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// InstallItem describes install information
type InstallItem struct {
	// ChartName defines the name for InstallItem
	ChartName string `json:"chartName,omitempty"`

	// ClientConfig defines the client config for InstallItem
	ClientConfig string `json:"clientConfig,omitempty"`

	// Overrides defines the overrides for InstallItem
	Overrides string `json:"overrides,omitempty"`
}

// ManifestCondition describes condition information for Manifest.
type ManifestCondition struct {
	//Type of ManifestCondition
	Type ManifestConditionType `json:"type"`

	// Status of the ManifestCondition.
	// Value can be one of ("True", "False", "Unknown").
	Status ManifestConditionStatus `json:"status"`

	// Human-readable message indicating details about the last status transition.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// Machine-readable text indicating the reason for the condition's last transition.
	// +kubebuilder:validation:Optional
	Reason string `json:"reason,omitempty"`

	// Timestamp for when Manifest last transitioned from one status to another.
	// +kubebuilder:validation:Optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// InstallInfo contains a list of installations for Manifest
	// +kubebuilder:validation:Optional
	InstallInfo InstallItem `json:"installInfo,omitempty"`
}

type ManifestConditionType string

const (
	// ConditionTypeReady represents ManifestConditionType Ready
	ConditionTypeReady ManifestConditionType = "Ready"
)

type ManifestConditionStatus string

// Valid ManifestCondition Status
const (
	// ConditionStatusTrue signifies ManifestConditionStatus true
	ConditionStatusTrue ManifestConditionStatus = "True"

	// ConditionStatusFalse signifies ManifestConditionStatus false
	ConditionStatusFalse ManifestConditionStatus = "False"

	// ConditionStatusUnknown signifies ManifestConditionStatus unknown
	ConditionStatusUnknown ManifestConditionStatus = "Unknown"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=".status.state"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Manifest is the Schema for the manifests API
type Manifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManifestSpec   `json:"spec,omitempty"`
	Status ManifestStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ManifestList contains a list of Manifest
type ManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Manifest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Manifest{}, &ManifestList{})
}
