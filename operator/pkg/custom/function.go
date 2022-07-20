package custom

import (
	"context"
	"k8s.io/client-go/rest"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckFnType func(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey,
	logger *logr.Logger, defaultRestConfig *rest.Config) (bool, error)

type Check interface {
	CheckFn(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey,
		logger *logr.Logger, defaultRestConfig *rest.Config) (bool, error)
}
