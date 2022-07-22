package types

type CustomState struct {
	// APIVersion defines api version of the custom resource
	APIVersion string `json:"apiVersion"`

	// Kind defines the kind of the custom resource
	Kind string `json:"kind"`

	// Name defines the name of the custom resource
	Name string `json:"name"`

	// Namespace defines the namespace of the custom resource
	Namespace string `json:"namespace"`

	// Namespace defines the desired state of the custom resource
	State string `json:"state"`
}
