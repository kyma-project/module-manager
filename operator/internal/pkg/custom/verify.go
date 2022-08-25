package custom

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"
	"github.com/kyma-project/module-installer/operator/pkg/custom"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	resource := manifestObj.Object["spec"].(map[string]interface{})["resource"].(*unstructured.Unstructured)
	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// check custom resource for states
	customStatus := &custom.Status{
		Reader: *remoteInfo.RemoteClient,
	}

	ready, err := customStatus.WaitForCustomResources(ctx, resource)
	if err != nil {
		logger.Error(err,
			fmt.Sprintf("error while tracking status of custom resources for manifest %s",
				namespacedName))
		return false, err
	}

	return ready, nil
}
