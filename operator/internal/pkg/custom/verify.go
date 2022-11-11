package custom

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Resource struct {
	DefaultClient client.Client
	types.Check
}

func (r *Resource) DefaultFn(context.Context, *unstructured.Unstructured, logr.Logger,
	types.ClusterInfo,
) (bool, error) {
	return true, nil
}

func (r *Resource) CheckFn(ctx context.Context, manifestObj *unstructured.Unstructured, logger logr.Logger,
	clusterInfo types.ClusterInfo,
) (bool, error) {
	// if manifest resource is in deleting state - validate check
	if !manifestObj.GetDeletionTimestamp().IsZero() {
		return true, nil
	}

	resource := manifestObj.Object["spec"].(map[string]interface{})["resource"].(*unstructured.Unstructured)
	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// check custom resource for states
	customStatus := &custom.Status{
		Reader: clusterInfo.Client,
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
