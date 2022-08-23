package declarative

import "github.com/kyma-project/manifest-operator/operator/pkg/types"

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

// WithObjectTransform adds the specified ObjectTransforms to the chain of manifest changes
func WithObjectTransform(operations ...types.ObjectTransform) reconcilerOption {
	return func(o manifestOptions) manifestOptions {
		if o.objectTransforms == nil {
			o.objectTransforms = []types.ObjectTransform{}
		}
		o.objectTransforms = append(o.objectTransforms, operations...)
		return o
	}
}
