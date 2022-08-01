package custom

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckFnType func(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey,
	logger *logr.Logger) (bool, error)

type Check interface {
	CheckFn(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey,
		logger *logr.Logger) (bool, error)
}
