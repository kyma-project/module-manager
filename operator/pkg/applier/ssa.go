package applier

import (
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

var _ manifestTypes.Applier = &SetApplier{}

const fieldManager = "manifest-lib"

type SetApplier struct {
	patchOptions  metav1.PatchOptions
	deleteOptions metav1.DeleteOptions
	logger        *logr.Logger
	mapper        *restmapper.DeferredDiscoveryRESTMapper
	dynamicClient dynamic.Interface
}

func NewSSAApplier(dynamicClient dynamic.Interface, logger *logr.Logger,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
) *SetApplier {
	return &SetApplier{
		patchOptions:  metav1.PatchOptions{FieldManager: fieldManager},
		logger:        logger,
		dynamicClient: dynamicClient,
		mapper:        mapper,
	}
}

func (s *SetApplier) Apply(deployInfo manifestTypes.InstallInfo, objects *manifestTypes.ManifestResources,
	namespace string,
) error {
	// Populate the namespace on any namespace-scoped objects
	err := s.adjustNs(objects, namespace)
	if err != nil {
		return err
	}

	// TODO: implement trackers for object statuses
	expectedLength := len(objects.Items)
	results, err := s.execute(deployInfo, objects.Items, s.dynamicClient, s.patchOptions)
	if err != nil {
		return fmt.Errorf("error applying objects: %w", err)
	}
	actualLength := len(results)

	if expectedLength != actualLength {
		s.logger.Info("not all resources could be applied via SSA",
			"expected", expectedLength,
			"actual", actualLength,
			"resource", client.ObjectKeyFromObject(deployInfo.BaseResource),
		)
	}

	return nil
}

func (s *SetApplier) Delete(deployInfo manifestTypes.InstallInfo, objects *manifestTypes.ManifestResources,
	namespace string,
) (bool, error) {
	// Populate the namespace on any namespace-scoped objects
	if err := s.adjustNs(objects, namespace); err != nil {
		return false, err
	}

	deletionSuccess := true
	deleteErrors := make([]error, 0)

	for _, obj := range objects.Items {
		name := obj.GetName()
		// get dynamic client interface for object
		resourceInterface, err := s.getDynamicResourceInterface(s.dynamicClient, obj)
		if err != nil {
			return false, err
		}

		if err = resourceInterface.Delete(deployInfo.Ctx, name, s.deleteOptions); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, err)
			deletionSuccess = true
		}
	}

	for _, deleteError := range deleteErrors {
		err := fmt.Errorf("%w/n", deleteError)
		s.logger.V(util.DebugLogLevel).Info("deletion of resource unsuccessful", "message",
			err.Error())
	}

	return deletionSuccess, nil
}

func (s *SetApplier) adjustNs(objects *manifestTypes.ManifestResources, namespace string) error {
	// Populate the namespace on any namespace-scoped objects
	if namespace != "" {
		for _, obj := range objects.Items {
			gvk := obj.GroupVersionKind()
			restMapping, err := s.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
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
	return nil
}

func (s *SetApplier) execute(deployInfo manifestTypes.InstallInfo, objects []*unstructured.Unstructured,
	dynamicClient dynamic.Interface, patchOptions metav1.PatchOptions,
) ([]*unstructured.Unstructured, error) {
	appliedObjects := make([]*unstructured.Unstructured, 0)

	applyErrors := make([]error, 0)
	for _, obj := range objects {
		name := obj.GetName()

		// get dynamic client interface for object
		resourceInterface, err := s.getDynamicResourceInterface(dynamicClient, obj)
		if err != nil {
			return nil, err
		}

		marshaledObject, err := json.Marshal(obj)
		if err != nil {
			// TODO: Differentiate between server-fixable vs client-fixable errors?
			applyErrors = append(applyErrors, fmt.Errorf("failed to marshal object to JSON: %w", err))
			continue
		}

		lastApplied, err := resourceInterface.Patch(deployInfo.Ctx, name, types.ApplyPatchType,
			marshaledObject, patchOptions)
		if err != nil {
			if !errors.IsConflict(err) {
				return nil, err
			}
			applyErrors = append(applyErrors, fmt.Errorf("error from apply: %w", err))
			continue
		}
		appliedObjects = append(appliedObjects, lastApplied)
	}

	var err error
	for _, applyError := range applyErrors {
		err = fmt.Errorf("%w/n", applyError)
		s.logger.V(util.DebugLogLevel).Info("conflict during SSA with no overwrites", "message",
			err.Error())
	}

	return appliedObjects, nil
}

func (s *SetApplier) getDynamicResourceInterface(dynamicClient dynamic.Interface, obj *unstructured.Unstructured,
) (dynamic.ResourceInterface, error) {
	var dynamicResource dynamic.ResourceInterface

	namespace := obj.GetNamespace()
	gvk := obj.GroupVersionKind()

	restMapping, err := s.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	gvr := restMapping.Resource

	switch restMapping.Scope.Name() {
	case meta.RESTScopeNameNamespace:
		if namespace == "" {
			return nil, fmt.Errorf("namespace was not provided for namespace-scoped object %v", gvk)
		}
		dynamicResource = dynamicClient.Resource(gvr).Namespace(namespace)

	case meta.RESTScopeNameRoot:
		if namespace != "" {
			// TODO: Differentiate between server-fixable vs client-fixable errors?
			return nil, fmt.Errorf(
				"namespace %q was provided for cluster-scoped object %v", obj.GetNamespace(), gvk)
		}
		dynamicResource = dynamicClient.Resource(gvr)

	default:
		// Internal error ... this is panic-level
		return nil, fmt.Errorf("unknown scope for gvk %s: %q", gvk, restMapping.Scope.Name())
	}
	return dynamicResource, nil
}
