package watch

import (
	"context"
	"fmt"

	"github.com/kyma-project/manifest-operator/operator/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type SKREventsHandler struct {
	client.Reader
}

func (h *SKREventsHandler) HandleSKREvents(ctx context.Context) *handler.Funcs {
	return &handler.Funcs{
		GenericFunc: func(evt event.GenericEvent, queue workqueue.RateLimitingInterface) {
			logger := log.FromContext(ctx)
			logger.WithName("listener").Info("Dispatching SKR event to the queue")
			evtObject := evt.Object
			namespacedName := client.ObjectKeyFromObject(evtObject)
			kymaName, ok := evtObject.GetLabels()[labels.ComponentOwner]
			if !ok {
				logger.WithValues(
					"resource", namespacedName.Name,
					"namespace", namespacedName.Namespace,
				).Error(nil, "failed to get kyma name for resource")
				return
			}
			manifestCRsForKymaName := &v1alpha1.ManifestList{}
			err := h.List(ctx, manifestCRsForKymaName, client.MatchingLabels{
				labels.ComponentOwner: kymaName,
			})
			if err != nil {
				logger.WithValues(
					"resource", namespacedName.Name,
					"namespace", namespacedName.Namespace,
				).Error(err, "failed to list manifest CRs")
				return
			}

			manifestCRForComponent, err := findManifestForResource(namespacedName, manifestCRsForKymaName.Items)
			if err != nil {
				logger.WithValues(
					"resource", namespacedName.Name,
					"namespace", namespacedName.Namespace,
				).Error(err, "failed to find manifest CR")
				return
			}

			queue.Add(ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(manifestCRForComponent),
			})
		},
	}
}

func findManifestForResource(namespacedName client.ObjectKey,
	manifests []v1alpha1.Manifest) (*v1alpha1.Manifest, error) {
	for _, manifestCR := range manifests {
		equalObjectKeyPredicate := manifestCR.Spec.Resource.GetName() == namespacedName.Name &&
			manifestCR.Spec.Resource.GetNamespace() == namespacedName.Namespace
		if equalObjectKeyPredicate {
			return &manifestCR, nil
		}
	}
	return nil, fmt.Errorf("corresponding manifest for requested component not found")
}
