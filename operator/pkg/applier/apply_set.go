package applier

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/log"

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
)

var _ manifestTypes.Applier = &SetApplier{}

type SetApplier struct {
	PatchOptions metav1.PatchOptions
}

func (a *SetApplier) Apply(deployInfo manifestTypes.InstallInfo, objects *manifestTypes.ManifestResources, namespace string) error {

	dynamicClient, err := dynamic.NewForConfig(deployInfo.Config)
	if err != nil {
		return fmt.Errorf("error building dynamic client: %w", err)
	}

	// Populate the namespace on any namespace-scoped objects
	if namespace != "" {
		for _, obj := range objects.Items {
			gvk := obj.GroupVersionKind()
			restMapping, err := deployInfo.RestMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				return fmt.Errorf("error getting rest mapping for %v: %w", gvk, err)
			}

			switch restMapping.Scope {
			case meta.RESTScopeNamespace:
				obj.SetNamespace(namespace)

			case meta.RESTScopeRoot:
				// Don't set namespace
			default:
				return fmt.Errorf("unknown rest mapping scope %v", restMapping.Scope)
			}
		}
	}

	// TODO: implement trackers for object statuses
	expectedLength := len(objects.Items)
	results, err := prepare(deployInfo, objects.Items, dynamicClient, a.PatchOptions)
	if err != nil {
		return fmt.Errorf("error applying objects: %w", err)
	}
	if expectedLength != len(results) {

	}

	return nil
}

func prepare(deployInfo manifestTypes.InstallInfo, objects []*unstructured.Unstructured,
	dynamicClient dynamic.Interface, patchOptions metav1.PatchOptions) ([]*unstructured.Unstructured, error) {

	appliedObject := make([]*unstructured.Unstructured, 0)

	applyErrors := make([]error, 0)
	for _, obj := range objects {

		name := obj.GetName()
		ns := obj.GetNamespace()
		gvk := obj.GroupVersionKind()

		restMapping, err := deployInfo.RestMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("error getting rest mapping for %v: %w", gvk, err))
			continue
		}
		gvr := restMapping.Resource

		var dynamicResource dynamic.ResourceInterface

		switch restMapping.Scope.Name() {
		case meta.RESTScopeNameNamespace:
			if ns == "" {
				applyErrors = append(applyErrors, fmt.Errorf("namespace was not provided for namespace-scoped object %v", gvk))
				continue
			}
			dynamicResource = dynamicClient.Resource(gvr).Namespace(ns)

		case meta.RESTScopeNameRoot:
			if ns != "" {
				// TODO: Differentiate between server-fixable vs client-fixable errors?
				applyErrors = append(applyErrors, fmt.Errorf(
					"namespace %q was provided for cluster-scoped object %v", obj.GetNamespace(), gvk))
				continue
			}
			dynamicResource = dynamicClient.Resource(gvr)

		default:
			// Internal error ... this is panic-level
			return nil, fmt.Errorf("unknown scope for gvk %s: %q", gvk, restMapping.Scope.Name())
		}

		marshaledObject, err := json.Marshal(obj)
		if err != nil {
			// TODO: Differentiate between server-fixable vs client-fixable errors?
			applyErrors = append(applyErrors, fmt.Errorf("failed to marshal object to JSON: %w", err))
			continue
		}

		lastApplied, err := dynamicResource.Patch(deployInfo.Ctx, name, types.ApplyPatchType,
			marshaledObject, patchOptions)
		if err != nil {
			if !errors.IsConflict(err) {
				return nil, err
			}
			applyErrors = append(applyErrors, fmt.Errorf("error from apply: %w", err))
			continue
		}
		appliedObject = append(appliedObject, lastApplied)
	}

	var err error
	for _, applyError := range applyErrors {
		err = fmt.Errorf("%w/n", applyError)
		log.Log.Info(err.Error())
	}

	return appliedObject, nil
}
