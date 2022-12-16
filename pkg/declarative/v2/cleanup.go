package v2

import (
	"context"

	"github.com/kyma-project/module-manager/pkg/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Cleanup interface {
	Run(context.Context, []*resource.Info) (bool, error)
}

type concurrentCleanup struct {
	clnt   client.Client
	policy client.PropagationPolicy
}

func ConcurrentCleanup(clnt client.Client) Cleanup {
	return &concurrentCleanup{clnt: clnt, policy: client.PropagationPolicy(metav1.DeletePropagationBackground)}
}

func (c *concurrentCleanup) Run(ctx context.Context, infos []*resource.Info) (bool, error) {
	// The Runtime Complexity of this Branch is N as only ServerSideApplier Patch is required
	results := make(chan error, len(infos))
	for i := range infos {
		i := i
		go c.cleanupResource(ctx, infos[i], results)
	}

	var errs []error
	present := len(infos)
	for i := 0; i < len(infos); i++ {
		err := <-results
		if apierrors.IsNotFound(err) {
			present--
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	// if present resources are present, return false
	// if present resources are 0, return true
	allDeleted := !(present > 0)

	if len(errs) > 0 {
		return false, types.NewMultiError(errs)
	}

	return allDeleted, nil
}

func (c *concurrentCleanup) cleanupResource(ctx context.Context, info *resource.Info, results chan error) {
	results <- c.clnt.Delete(ctx, info.Object.(client.Object), c.policy)
}
