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
	"github.com/kyma-project/manifest-operator/operator/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const ManifestKind = "Manifest"

func (m *Manifest) SetObservedGeneration() *Manifest {
	m.Status.ObservedGeneration = m.Generation
	return m
}

// InstallInfo defines installation information
type InstallInfo struct {
	// Source can either be described as ImageSpec or HelmChartSpec
	//+kubebuilder:pruning:PreserveUnknownFields
	Source runtime.RawExtension `json:"source"`

	// Name specifies a unique install name for Manifest
	Name string `json:"name"`
}

// ManifestSpec defines the specification of Manifest
type ManifestSpec struct {
	// Config specifies OCI image configuration for Manifest
	// +kubebuilder:validation:Optional
	Config types.ImageSpec `json:"config"`

	// Installs specifies a list of installations for Manifest
	Installs []InstallInfo `json:"installs"`

	// CustomStates specifies a list of resources with their desires states for Manifest
	// +kubebuilder:validation:Optional
	CustomStates []types.CustomState `json:"customStates"`

	//+kubebuilder:pruning:PreserveUnknownFields
	//+kubebuilder:object:generate=false
	// Resource specifies a resource to be watched for state updates
	Resource unstructured.Unstructured `json:"resource"`

	// CRDs specifies the custom resource definitions' ImageSpec
	// +kubebuilder:validation:Optional
	CRDs types.ImageSpec `json:"crds"`
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
	// State signifies current state of Manifest
	// +kubebuilder:validation:Enum=Ready;Processing;Error;Deleting;
	State ManifestState `json:"state"`

	// Conditions is a list of status conditions to indicate the status of Manifest
	// +kubebuilder:validation:Optional
	Conditions []ManifestCondition `json:"conditions"`

	// ObservedGeneration
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration"`
}

// InstallItem describes install information for ManifestCondition
type InstallItem struct {
	// ChartName defines the name for InstallItem
	// +kubebuilder:validation:Optional
	ChartName string `json:"chartName"`

	// ClientConfig defines the client config for InstallItem
	// +kubebuilder:validation:Optional
	ClientConfig string `json:"clientConfig"`

	// Overrides defines the overrides for InstallItem
	// +kubebuilder:validation:Optional
	Overrides string `json:"overrides"`
}

// ManifestCondition describes condition information for Manifest.
type ManifestCondition struct {
	// Type of ManifestCondition
	Type ManifestConditionType `json:"type"`

	// Status of the ManifestCondition
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status ManifestConditionStatus `json:"status"`

	// Human-readable message indicating details about the last status transition.
	// +kubebuilder:validation:Optional
	Message string `json:"message"`

	// Machine-readable text indicating the reason for the condition's last transition.
	// +kubebuilder:validation:Optional
	Reason string `json:"reason"`

	// Timestamp for when Manifest last transitioned from one status to another.
	// +kubebuilder:validation:Optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime"`

	// InstallInfo contains a list of installations for Manifest
	// +kubebuilder:validation:Optional
	InstallInfo InstallItem `json:"installInfo"`
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
	metav1.ObjectMeta `json:"metadata"`

	// Spec specifies the content and configuration for Manifest
	Spec ManifestSpec `json:"spec"`

	// Status signifies the current status of the Manifest
	// +kubebuilder:validation:Optional
	Status ManifestStatus `json:"status"`
}

//+kubebuilder:object:root=true

// ManifestList contains a list of Manifest
type ManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Manifest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Manifest{}, &ManifestList{})
}
