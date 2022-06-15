package custom

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Status struct {
	client.Reader
}

func (c *Status) WaitForCustomResources(ctx context.Context, customWaitResource []v1alpha1.CustomState) (bool, error) {
	for _, res := range customWaitResource {
		obj := unstructured.Unstructured{}
		obj.SetAPIVersion(res.APIVersion)
		obj.SetKind(res.Kind)
		namespacedName := client.ObjectKey{Name: res.Name, Namespace: res.Namespace}
		if err := c.Get(ctx, namespacedName, &obj); client.IgnoreNotFound(err) != nil {
			return false, err
		}

		status, ok := obj.Object["status"]
		if !ok {
			return false, fmt.Errorf("status object not found for %s", namespacedName.String())
		}

		state, ok := status.(map[string]interface{})["state"]
		if !ok {
			return false, fmt.Errorf("state not found for %s", namespacedName.String())
		}

		if state.(string) != res.State {
			return false, nil
		}
	}

	return true, nil
}
