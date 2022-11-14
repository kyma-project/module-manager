package applier

import (
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	machineryTypes "k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/client"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

var _ types.Applier = &SetApplier{}

const fieldManager = "manifest-lib"

type SetApplier struct {
	patchOptions  metav1.PatchOptions
	deleteOptions metav1.DeleteOptions
	logger        logr.Logger
	clients       *client.SingletonClients
}

func NewSSAApplier(clients *client.SingletonClients, logger logr.Logger) *SetApplier {
	force := true
	return &SetApplier{
		patchOptions: metav1.PatchOptions{FieldManager: fieldManager, Force: &force},
		logger:       logger,
		clients:      clients,
	}
}

func (s *SetApplier) Apply(deployInfo types.InstallInfo, objects *types.ManifestResources,
	namespace string,
) (bool, error) {
	// Populate the namespace on any namespace-scoped objects
	err := s.adjustNs(objects, namespace)
	if err != nil {
		return false, err
	}

	// TODO: implement trackers for object statuses
	expectedLength := len(objects.Items)
	results, err := s.execute(deployInfo, objects.Items)
	if err != nil {
		return false, fmt.Errorf("error applying objects via SSA: %w", err)
	}

	// actualLength also includes resources with a conflict
	// since they already exist and a no conflict resolution is followed
	// we will assume they were applied correctly
	if expectedLength != len(results) {
		s.logger.Info("not all resources were applied via SSA",
			"expected", expectedLength,
			"actual", len(results),
			"resource", ctrlclient.ObjectKeyFromObject(deployInfo.BaseResource),
		)
	}

	return expectedLength == len(results), nil
}

func (s *SetApplier) Delete(deployInfo types.InstallInfo, objects *types.ManifestResources,
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
		resourceInterface, err := s.clients.DynamicResourceInterface(obj)
		if err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to get rest mapping for resource %s: %w",
				ctrlclient.ObjectKeyFromObject(obj).String(), err))
			continue
		}

		if err = resourceInterface.Delete(deployInfo.Ctx, name, s.deleteOptions); err != nil && !apiErrors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, err)
			deletionSuccess = false
		}
	}

	for _, deleteError := range deleteErrors {
		err := fmt.Errorf("%w/n", deleteError)
		s.logger.V(util.DebugLogLevel).Info("deletion of resource unsuccessful", "message",
			err.Error())
	}

	return deletionSuccess, nil
}

func (s *SetApplier) adjustNs(objects *types.ManifestResources, namespace string) error {
	if namespace == "" {
		return nil
	}

	// Populate the namespace on any namespace-scoped objects
	mapper, err := s.clients.ToRESTMapper()
	if err != nil {
		return err
	}

	for _, obj := range objects.Items {
		gvk := obj.GroupVersionKind()
		var restMapping *meta.RESTMapping

		restMapping, err = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			if resettableMapper, isResettable := mapper.(meta.ResettableRESTMapper); isResettable &&
				meta.IsNoMatchError(err) {
				resettableMapper.Reset()
			}
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

	return nil
}

func (s *SetApplier) execute(
	deployInfo types.InstallInfo,
	objects []*unstructured.Unstructured,
) ([]*unstructured.Unstructured, error) {
	appliedObjects := make([]*unstructured.Unstructured, 0)

	applyErrors := make([]error, 0)
	for _, obj := range objects {
		name := obj.GetName()

		// get dynamic client interface for object
		resourceInterface, err := s.clients.DynamicResourceInterface(obj)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to get rest mapping for resource %s: %w",
				ctrlclient.ObjectKeyFromObject(obj).String(), err))
			continue
		}

		marshaledObject, err := json.Marshal(obj)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to marshal object to JSON: %w", err))
			continue
		}

		obj, err = resourceInterface.Patch(deployInfo.Ctx, name, machineryTypes.ApplyPatchType,
			marshaledObject, s.patchOptions)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("error from apply: %w", err))
			continue
		}
		appliedObjects = append(appliedObjects, obj)
	}

	var err error
	for _, applyError := range applyErrors {
		err = fmt.Errorf("%w/n", applyError)
		s.logger.V(util.DebugLogLevel).Info("conflict during SSA with no overwrites", "message",
			err.Error())
	}

	if len(applyErrors) > 0 {
		return appliedObjects, types.NewMultiError(applyErrors)
	}

	return appliedObjects, nil
}
