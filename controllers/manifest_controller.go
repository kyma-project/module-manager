package controllers

import (
	"fmt"

	"github.com/kyma-project/module-manager/api/v1alpha1"
	internalv1alpha1 "github.com/kyma-project/module-manager/internal/manifest/v1alpha1"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func SetupWithManager(
	mgr manager.Manager,
	eventChannel source.Source,
	codec *types.Codec,
	options controller.Options,
	insecure bool,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Manifest{}).
		Watches(&source.Kind{Type: &v1.Secret{}}, handler.Funcs{}).
		Watches(
			eventChannel, &handler.Funcs{
				GenericFunc: func(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
					ctrl.Log.WithName("listener").Info(
						fmt.Sprintf(
							"event coming from SKR, adding %s to queue",
							client.ObjectKeyFromObject(event.Object).String(),
						),
					)
					queue.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(event.Object)})
				},
			},
		).WithOptions(options).Complete(ManifestReconciler(mgr, codec, insecure))
}

func ManifestReconciler(mgr manager.Manager, codec *types.Codec, insecure bool) *declarative.Reconciler {
	return declarative.NewFromManager(
		mgr, &v1alpha1.Manifest{},
		declarative.WithSpecResolver(
			internalv1alpha1.NewManifestSpecResolver(codec, insecure),
		),
		declarative.WithRemoteTargetCluster(
			(&internalv1alpha1.RemoteClusterLookup{KCP: &types.ClusterInfo{
				Client: mgr.GetClient(),
				Config: mgr.GetConfig(),
			}}).ConfigResolver,
		),
		declarative.WithClientCacheKeyFromLabelOrResource(labels.KymaName),
		declarative.WithPostRun{internalv1alpha1.PostRunCreateCR},
		declarative.WithPreDelete{internalv1alpha1.PreDeleteDeleteCR},
	)
}
