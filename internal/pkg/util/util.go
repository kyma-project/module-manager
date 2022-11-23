package util

import (
	"encoding/json"
	"time"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/internal/pkg/types"
	opLabels "github.com/kyma-project/module-manager/pkg/labels"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/pkg/util"
)

func getReadyConditionForComponent(manifest *v1alpha1.Manifest,
	installName string,
) (*v1alpha1.ManifestCondition, bool) {
	status := &manifest.Status
	for _, existingCondition := range status.Conditions {
		if existingCondition.Type == v1alpha1.ConditionTypeReady && existingCondition.Reason == installName {
			return &existingCondition, true
		}
	}
	return &v1alpha1.ManifestCondition{}, false
}

func AddReadyConditionForObjects(manifest *v1alpha1.Manifest, installItems []v1alpha1.InstallItem,
	conditionStatus v1alpha1.ManifestConditionStatus, message string,
) {
	status := &manifest.Status
	for _, installItem := range installItems {
		condition, exists := getReadyConditionForComponent(manifest, installItem.ChartName)
		if !exists {
			condition = &v1alpha1.ManifestCondition{
				Type:   v1alpha1.ConditionTypeReady,
				Reason: installItem.ChartName,
			}
			status.Conditions = append(status.Conditions, *condition)
		}
		condition.LastTransitionTime = &metav1.Time{Time: time.Now()}
		condition.Message = message
		condition.Status = conditionStatus
		if installItem.ClientConfig != "" || installItem.Overrides != "" {
			condition.InstallInfo = installItem
		}

		for i, existingCondition := range status.Conditions {
			if existingCondition.Type == v1alpha1.ConditionTypeReady &&
				existingCondition.Reason == installItem.ChartName {
				status.Conditions[i] = *condition
				break
			}
		}
	}
}

func AddReadyConditionForResponses(responses []*types.InstallResponse, logger logr.Logger,
	manifest *v1alpha1.Manifest,
) {
	namespacedName := client.ObjectKeyFromObject(manifest)
	for _, response := range responses {
		status := v1alpha1.ConditionStatusTrue
		message := "installation successful"

		if response.Err != nil {
			status = v1alpha1.ConditionStatusFalse
			message = "installation error"
		} else if !response.Ready {
			status = v1alpha1.ConditionStatusUnknown
			message = "installation processing"
		}

		configBytes, err := json.Marshal(response.Flags.ConfigFlags)
		if err != nil {
			logger.V(util.DebugLogLevel).Error(err, "error marshalling chart config for",
				"resource", namespacedName)
		}

		overrideBytes, err := json.Marshal(response.Flags.SetFlags)
		if err != nil {
			logger.V(util.DebugLogLevel).Error(err, "error marshalling chart values for",
				"resource", namespacedName)
		}

		AddReadyConditionForObjects(manifest, []v1alpha1.InstallItem{{
			ClientConfig: string(configBytes),
			Overrides:    string(overrideBytes),
			ChartName:    response.ChartName,
		}}, status, message)
	}
}

func GetCacheFunc() cache.NewCacheFunc {
	return cache.BuilderWithOptions(cache.Options{
		SelectorsByObject: cache.SelectorsByObject{
			&v1.Secret{}: {
				Label: labels.SelectorFromSet(
					labels.Set{opLabels.ManagedBy: opLabels.LifecycleManager},
				),
			},
		},
	})
}
