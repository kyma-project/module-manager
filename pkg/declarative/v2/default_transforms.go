package v2

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	DisclaimerAnnotation      = "reconciler.kyma-project.io/managed-by-reconciler-disclaimer"
	disclaimerAnnotationValue = "DO NOT EDIT - This resource is managed by Kyma.\n" +
		"Any modifications are discarded and the resource is reverted to the original state."
	ManagedByLabel      = "reconciler.kyma-project.io/managed-by"
	managedByLabelValue = "declarative-v2"
)

func disclaimerTransform(_ context.Context, _ Object, resources []*unstructured.Unstructured) error {
	for _, resource := range resources {
		annotations := resource.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[DisclaimerAnnotation] = disclaimerAnnotationValue
		resource.SetAnnotations(annotations)
	}
	return nil
}

func kymaComponentTransform(_ context.Context, obj Object, resources []*unstructured.Unstructured) error {
	for _, resource := range resources {
		labels := resource.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["app.kubernetes.io/component"] = obj.ComponentName()
		labels["app.kubernetes.io/part-of"] = "Kyma"
		resource.SetLabels(labels)
	}
	return nil
}

func managedByDeclarativeV2(_ context.Context, _ Object, resources []*unstructured.Unstructured) error {
	for _, resource := range resources {
		labels := resource.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		// legacy managed by value
		labels[ManagedByLabel] = managedByLabelValue
		resource.SetLabels(labels)
	}
	return nil
}
