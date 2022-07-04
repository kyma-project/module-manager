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

type CustomState struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	State      string `json:"state,omitempty"`
}

// InstallInfo defines installation information
type InstallInfo struct {
	OCIRef `json:",inline"`
	Type   string `json:"type"`
	Name   string `json:"name"`
}

// OCIRef defines OCI image configuration
type OCIRef struct {
	Repo    string `json:"repo"`
	Module  string `json:"module"`
	Tag     string `json:"tag,omitempty"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// ManifestSpec defines the specification of Manifest
type ManifestSpec struct {
	// OCIRef specifies OCI image configuration for Manifest
	// +optional
	OCIRef OCIRef `json:"ociRef,omitempty"`

	// Installs specifies a list of installations for Manifest
	Installs []InstallInfo `json:"installs,omitempty"`

	// CustomStates specifies a list of resources with their desires states for Manifest
	// +optional
	CustomStates []CustomState `json:"customStates,omitempty"`

	// Sync specifies the sync strategy for Manifest
	// +optional
	Sync Sync `json:"sync,omitempty"`
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
	// ConditionStatusTrue signifies ManifestConditionStatus true
	ConditionStatusTrue ManifestConditionStatus = "True"

	// ConditionStatusFalse signifies ManifestConditionStatus false
	ConditionStatusFalse ManifestConditionStatus = "False"

	// ConditionStatusUnknown signifies ManifestConditionStatus unknown
	ConditionStatusUnknown ManifestConditionStatus = "Unknown"
)

type SyncStrategy string

const (
	SyncStrategyRemoteSecret SyncStrategy = "remote-secret"
	SyncStrategyLocalSecret  SyncStrategy = "local-secret"
)

// Sync defines settings used to apply the manifest synchronization to other clusters
type Sync struct {
	// +kubebuilder:default:=true
	// Enabled set to true will look up a kubeconfig for the remote cluster based on the strategy
	// and synchronize its state there.
	Enabled bool `json:"enabled,omitempty"`

	// Strategy determines the way to lookup the remotely synced kubeconfig, by default it is fetched from a secret
	Strategy SyncStrategy `json:"strategy,omitempty"`

	// The target namespace, if empty the namespace is reflected from the control plane
	// Note that cleanup is currently not supported if you are switching the namespace, so you will
	// manually need to cleanup old synchronized Manifests
	Namespace string `json:"namespace,omitempty"`
}

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
