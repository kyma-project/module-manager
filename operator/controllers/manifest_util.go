package controllers

import (
	"time"

	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func addReadyConditionForObjects(manifest *v1alpha1.Manifest, installItems []v1alpha1.InstallItem,
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
