package custom

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/pkg/custom"
	"github.com/kyma-project/module-manager/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrResourceUnstructuredExtraction = errors.New("unstructured could not be extracted")

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

	resource, ok := manifestObj.Object["spec"].(map[string]interface{})["resource"].(map[string]interface{})
	if !ok {
		return false, ErrResourceUnstructuredExtraction
	}
	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// check custom resource for states
	customStatus := &custom.Status{
		Reader: clusterInfo.Client,
	}

	ready, err := customStatus.WaitForCustomResources(ctx, &unstructured.Unstructured{Object: resource})
	if err != nil {
		logger.Error(err,
			fmt.Sprintf("error while tracking status of custom resources for manifest %s",
				namespacedName))
		return false, err
	}

	return ready, nil
}
