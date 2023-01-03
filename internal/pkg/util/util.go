package util

import (
	opLabels "github.com/kyma-project/module-manager/pkg/labels"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

func GetCacheFunc() cache.NewCacheFunc {
	return cache.BuilderWithOptions(
		cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&v1.Secret{}: {
					Label: labels.SelectorFromSet(
						labels.Set{opLabels.ManagedBy: opLabels.LifecycleManager},
					),
				},
			},
		},
	)
}
