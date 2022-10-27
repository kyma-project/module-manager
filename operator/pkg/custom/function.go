package custom

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type CheckFnType func(context.Context, *unstructured.Unstructured, *logr.Logger, types.ClusterInfo) (bool, error)

type Check interface {
	CheckFn(context.Context, *unstructured.Unstructured, *logr.Logger, types.ClusterInfo) (bool, error)
	DefaultFn(context.Context, *unstructured.Unstructured, *logr.Logger, types.ClusterInfo) (bool, error)
}
