package custom

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Status struct {
	client.Reader
}

type WaitResource struct {
	schema.GroupVersionKind
	client.ObjectKey
	ResStatus string
}

func (c *Status) WaitForCustomResources(ctx context.Context, customWaitResource []WaitResource) (bool, error) {
	for _, res := range customWaitResource {
		obj := unstructured.Unstructured{}
		obj.SetGroupVersionKind(res.GroupVersionKind)

		if err := c.Get(ctx, res.ObjectKey, &obj); client.IgnoreNotFound(err) != nil {
			return false, err
		}

		status, ok := obj.Object["status"]
		if !ok {
			return false, fmt.Errorf("status object not found for %s", res.ObjectKey.String())
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
