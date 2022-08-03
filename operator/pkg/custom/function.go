package custom

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckFnType func(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey,
	logger *logr.Logger) (bool, error)

type Check interface {
	CheckFn(context.Context, map[string]string, client.ObjectKey, *logr.Logger) (bool, error)
	DefaultFn(context.Context, map[string]string, client.ObjectKey, *logr.Logger) (bool, error)
}
