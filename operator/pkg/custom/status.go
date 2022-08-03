package custom

import (
	"context"

	"github.com/kyma-project/manifest-operator/operator/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Status struct {
	client.Reader
}

// TODO: Define centrally.
const readyState = "Ready"

func (c *Status) WaitForCustomResources(ctx context.Context, customWaitResource []types.CustomState,
	stateResource *unstructured.Unstructured,
) (bool, error) {
	// custom states
	for _, res := range customWaitResource {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(res.APIVersion)
		obj.SetKind(res.Kind)
		namespacedName := client.ObjectKey{Name: res.Name, Namespace: res.Namespace}

		// if not found or any misc error occurred - return!
		if err := c.Get(ctx, namespacedName, obj); err != nil {
			return false, err
		}

		state, err := getStateFieldFromUnstructured(obj)
		if err != nil {
			return false, err
		}

		if state != res.State {
			return false, nil
		}
	}

	// resource
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
