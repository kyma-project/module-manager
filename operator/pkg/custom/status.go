package custom

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Status struct {
	client.Reader
}

// TODO: Define centrally.
const readyState = "Ready"

func (c *Status) WaitForCustomResources(ctx context.Context, stateResource *unstructured.Unstructured,
) (bool, error) {
	// unstructured resource containing .status.state
	if stateResource != nil {
		existingStateResource := &unstructured.Unstructured{}
		existingStateResource.SetGroupVersionKind(stateResource.GroupVersionKind())
		customResourceKey := client.ObjectKey{
			Name:      stateResource.GetName(),
			Namespace: stateResource.GetNamespace(),
		}

		// if not found or any misc error occurred - return!
		if err := c.Get(ctx, customResourceKey, existingStateResource); err != nil {
			return false, err
		}

		state, err := getStateFieldFromUnstructured(existingStateResource)
		if err != nil {
			return false, err
		}

		if state != readyState {
			return false, nil
		}
	}

	return true, nil
}

func getStateFieldFromUnstructured(resource *unstructured.Unstructured) (string, error) {
	path := field.NewPath("status")
	status, statusExists := resource.Object["status"]
	if !statusExists {
		return "", field.NotFound(path, resource.Object)
	}

	path = path.Child("state")
	state, stateExists := status.(map[string]interface{})["state"]
	if !stateExists {
		return "", field.NotFound(path, resource.Object)
	}
	return state.(string), nil
}
