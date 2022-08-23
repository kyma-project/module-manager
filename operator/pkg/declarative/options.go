package declarative

import "github.com/kyma-project/manifest-operator/operator/pkg/types"

// WithResourceLabels adds the specified labels to the list of labels for the reconciled resource
func WithResourceLabels(labels map[string]string) reconcilerOption {
	return func(o manifestOptions) manifestOptions {
		if o.resourceLabels == nil {
			o.resourceLabels = make(map[string]string, 0)
		}
		for key, value := range labels {
			o.resourceLabels[key] = value
		}

		return o
	}
}

// WithObjectTransform adds the specified ObjectTransforms to the list of manifest resource changes
func WithObjectTransform(operations ...types.ObjectTransform) reconcilerOption {
	return func(o manifestOptions) manifestOptions {
		if o.objectTransforms == nil {
			o.objectTransforms = []types.ObjectTransform{}
		}
		o.objectTransforms = append(o.objectTransforms, operations...)
		return o
	}
}
