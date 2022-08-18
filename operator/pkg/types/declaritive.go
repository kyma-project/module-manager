package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type CustomObject interface {
	runtime.Object
	metav1.Object
	ComponentName() string
	GetSpec() CustomObjectSpec
	GetStatus() CustomObjectStatus
	SetStatus(CustomObjectStatus)
}

// +k8s:deepcopy-gen=true

// CustomObjectSpec specifies spec for CustomObject.
type CustomObjectSpec struct {
	// ChartPath specifies path to local helm chart.
	ChartPath string `json:"chartPath,omitempty"`

	// ReleaseName specifies release name for helm chart.
	ReleaseName string `json:"releaseName,omitempty"`

	// ChartFlags specifies comma seperated flags for chart installation.
	ChartFlags string `json:"chartFlags,omitempty"`

	// SetFlag specifies comma seperated values for --set flag.
	SetValues string `json:"setValues,omitempty"`
}

type CustomState string

// Valid CustomObject States.
const (
	// CustomStateReady signifies CustomObject is ready and has been installed successfully.
	CustomStateReady CustomState = "Ready"

	// CustomStateProcessing signifies CustomObject is reconciling and is in the process of installation.
	// Processing can also signal that the Installation previously encountered an error and is now recovering.
	CustomStateProcessing CustomState = "Processing"

	// CustomStateError signifies an error for CustomObject. This signifies that the Installation
	// process encountered an error.
	// Contrary to Processing, it can be expected that this state should change on the next retry.
	CustomStateError CustomState = "Error"

	// CustomStateDeleting signifies CustomObject is being deleted. This is the state that is used
	// when a deletionTimestamp was detected and Finalizers are picked up.
	CustomStateDeleting CustomState = "Deleting"
)

// +k8s:deepcopy-gen=true

// CustomObjectStatus defines the observed state of CustomObject
type CustomObjectStatus struct {
	// State signifies current state of CustomObject.
	// Value can be one of ("Ready", "Processing", "Error", "Deleting").
	State CustomState `json:"state,omitempty"`

	// Conditions associated with CustomStatus.
	Conditions []*metav1.Condition `json:"conditions,omitempty"`
}
