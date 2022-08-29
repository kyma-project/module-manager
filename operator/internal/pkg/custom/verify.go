package custom

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrSpecResourceCastFailed = errors.New("spec.resource case assertion failed")
var ErrSpecCastFailed = errors.New("spec case assertion failed")

type Resource struct {
	DefaultClient client.Client
	custom.Check
}

func (r *Resource) DefaultFn(context.Context, *unstructured.Unstructured, *logr.Logger,
	custom.RemoteInfo,
) (bool, error) {
	return true, nil
}

func (r *Resource) CheckFn(ctx context.Context, manifestObj *unstructured.Unstructured, logger *logr.Logger,
	remoteInfo custom.RemoteInfo,
) (bool, error) {
	spec, found := manifestObj.Object["spec"].(map[string]interface{})
	if !found {
		return false, ErrSpecCastFailed
	}
	resourceMap, found := spec["resource"].(map[string]interface{})
	if !found {
		return false, ErrSpecResourceCastFailed
	}
	resource := &unstructured.Unstructured{}
	resource.SetUnstructuredContent(resourceMap)

	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// check custom resource for states
	customStatus := &custom.Status{
		Reader: *remoteInfo.RemoteClient,
	}

	ready, err := customStatus.WaitForCustomResources(ctx, resource)
	if client.IgnoreNotFound(err) != nil {
		logger.Error(err,
			fmt.Sprintf("error while tracking status of custom resources for manifest %s",
				namespacedName))
		return false, err
	}

	return ready, nil
}
