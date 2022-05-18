package controllers

import (
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

const manifestFinalizer = "component.kyma-project.io/manifest"

func getReadyConditionForComponent(kymaObj *v1alpha1.Manifest, componentName string) (*v1alpha1.ManifestCondition, bool) {
	status := &kymaObj.Status
	for _, existingCondition := range status.Conditions {
		if existingCondition.Type == v1alpha1.ConditionTypeReady && existingCondition.Reason == componentName {
			return &existingCondition, true
		}
	}
	return &v1alpha1.ManifestCondition{}, false
}

func addReadyConditionForObjects(kymaObj *v1alpha1.Manifest, componentNames []string, conditionStatus v1alpha1.ManifestConditionStatus, message string) {
	status := &kymaObj.Status
	for _, componentName := range componentNames {
		condition, exists := getReadyConditionForComponent(kymaObj, componentName)
		if !exists {
			condition = &v1alpha1.ManifestCondition{
				Type:   v1alpha1.ConditionTypeReady,
				Reason: componentName,
			}
			status.Conditions = append(status.Conditions, *condition)
		}
		condition.LastTransitionTime = &metav1.Time{Time: time.Now()}
		condition.Message = message
		condition.Status = conditionStatus

		for i, existingCondition := range status.Conditions {
			if existingCondition.Type == v1alpha1.ConditionTypeReady && existingCondition.Reason == componentName {
				status.Conditions[i] = *condition
				break
			}
		}
	}
}
