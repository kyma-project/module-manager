package declarative

import "github.com/kyma-project/manifest-operator/operator/pkg/types"

// WithResourceLabels adds the specified labels to the list of labels for the reconciled resource.
func WithResourceLabels(labels map[string]string) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		for key, value := range labels {
			allOptions.resourceLabels[key] = value
		}

		return allOptions
	}
}

// WithObjectTransform adds the specified ObjectTransforms to the list of manifest resource changes.
func WithObjectTransform(operations ...types.ObjectTransform) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		allOptions.objectTransforms = append(allOptions.objectTransforms, operations...)
		return allOptions
	}
}
