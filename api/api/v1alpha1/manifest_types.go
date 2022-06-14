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
)

const ManifestKind = "Manifest"

func (manifest *Manifest) SetObservedGeneration() *Manifest {
	manifest.Status.ObservedGeneration = manifest.Generation
	return manifest
}

type CustomResState struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	State     string `json:"state,omitempty"`
}

// ChartInfo defines chart information
type ChartInfo struct {
	ChartPath    string `json:"chartPath,omitempty"`
	RepoName     string `json:"repoName,omitempty"`
	Url          string `json:"url,omitempty"`
	ChartName    string `json:"chartName,omitempty"`
	ReleaseName  string `json:"releaseName,omitempty"`
	ClientConfig string `json:"clientConfig,omitempty"`
}

// ManifestSpec defines the specification of Manifest
type ManifestSpec struct {
	Charts          []ChartInfo      `json:"charts,omitempty"`
	CustomResStates []CustomResState `json:"customResStates,omitempty"`
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
	// +optional
	Conditions []ManifestCondition `json:"conditions,omitempty"`

	// Observed generation
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ManifestCondition describes condition information for Manifest.
type ManifestCondition struct {
	//Type of ManifestCondition
	Type ManifestConditionType `json:"type"`

	// Status of the ManifestCondition.
	// Value can be one of ("True", "False", "Unknown").
	Status ManifestConditionStatus `json:"status"`

	// Human-readable message indicating details about the last status transition.
	// +optional
	Message string `json:"message,omitempty"`

	// Machine-readable text indicating the reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Timestamp for when Manifest last transitioned from one status to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

type ManifestConditionType string

const (
	// ConditionTypeReady represents ManifestConditionType Ready
	ConditionTypeReady ManifestConditionType = "Ready"
)

type ManifestConditionStatus string

// Valid ManifestCondition Status
const (
	// ConditionStatusTrue signifies KymaConditionStatus true
	ConditionStatusTrue ManifestConditionStatus = "True"

	// ConditionStatusFalse signifies KymaConditionStatus false
	ConditionStatusFalse ManifestConditionStatus = "False"

	// ConditionStatusUnknown signifies KymaConditionStatus unknown
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
