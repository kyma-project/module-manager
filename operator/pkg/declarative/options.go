package declarative

import "github.com/kyma-project/module-manager/operator/pkg/types"

// WithCustomResourceLabels adds the specified labels to the list of labels for the reconciled resource.
func WithCustomResourceLabels(labels map[string]string) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		for key, value := range labels {
			allOptions.resourceLabels[key] = value
		}

		return allOptions
	}
}

// WithPostRenderTransform adds the specified ObjectTransforms to the list of manifest resource changes.
func WithPostRenderTransform(operations ...types.ObjectTransform) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		allOptions.objectTransforms = append(allOptions.objectTransforms, operations...)
		return allOptions
	}
}

// WithManifestResolver resolves manifest object for a given object instance.
func WithManifestResolver(resolver types.ManifestResolver) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		allOptions.manifestResolver = resolver
		return allOptions
	}
}

// WithDefaultResolver resolves manifest object using the default resolver.
// This resolver parses the `spec` of the given object into an InstallationSpec.
func WithDefaultResolver() ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		allOptions.manifestResolver = DefaultManifestResolver{}
		return allOptions
	}
}

// WithResourcesReady verifies if native resources are in their respective ready states.
func WithResourcesReady(verify bool) ReconcilerOption {
	return func(allOptions manifestOptions) manifestOptions {
		allOptions.verify = verify
		return allOptions
	}
}
