package custom

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CustomStatus struct {
	client.Reader
}

type CustomWaitResource struct {
	schema.GroupVersionKind
	client.ObjectKey
	ResStatus string
}

func (c *CustomStatus) WaitForCustomResources(ctx context.Context, customWaitResource []CustomWaitResource) (bool, error) {
	for _, res := range customWaitResource {
		obj := unstructured.Unstructured{}
		obj.SetGroupVersionKind(res.GroupVersionKind)

		if err := c.Get(ctx, res.ObjectKey, &obj); client.IgnoreNotFound(err) != nil {
			return false, err
		}

		status, ok := obj.Object["custom"]
		if !ok {
			return false, fmt.Errorf("custom object not found for %s", res.ObjectKey.String())
		}

		state, ok := status.(map[string]interface{})["state"]
		if !ok {
			return false, fmt.Errorf("state not found for %s", res.ObjectKey.String())
		}

		if state.(string) != res.ResStatus {
			return false, nil
		}
	}

	return true, nil
}
