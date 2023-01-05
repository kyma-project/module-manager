package controllers

import (
	"context"
	"fmt"

	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"github.com/kyma-project/module-manager/pkg/labels"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func WatchedByOwnedBy(
	_ context.Context, obj declarative.Object, unstructs []*unstructured.Unstructured,
) error {
	for _, unstruct := range unstructs {
		if unstruct.GetLabels() == nil {
			unstruct.SetLabels(map[string]string{})
		}
		unstruct.GetLabels()[labels.WatchedByLabel] = labels.OperatorName
		unstruct.GetLabels()[labels.OwnedByLabel] = fmt.Sprintf(
			labels.OwnedByFormat, obj.GetNamespace(), obj.GetName(),
		)
	}
	return nil
}
