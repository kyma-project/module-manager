package declarative

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

var _ reconcile.Reconciler = &ManifestReconciler{}

const (
	requeueInterval = time.Second * 3
)

type ManifestReconciler struct {
	prototype    types.BaseCustomObject
	nativeClient client.Client
	config       *rest.Config

	mgr manager.Manager

	// recorder is the EventRecorder for creating k8s events
	recorder record.EventRecorder
	options  manifestOptions
}

type manifestOptions struct {
	force            bool
	verify           bool
	resourceLabels   map[string]string
	objectTransforms []types.ObjectTransform
	manifestResolver types.ManifestResolver
	finalizer        string
}

func (m *manifestOptions) isFinalizerSet() bool {
	return m.finalizer != ""
}

type ReconcilerOption func(manifestOptions) manifestOptions

func (r *ManifestReconciler) Inject(mgr manager.Manager, customObject types.BaseCustomObject,
	opts ...ReconcilerOption,
) error {
	r.prototype = customObject
	r.config = mgr.GetConfig()
	r.mgr = mgr
	controllerName, err := GetComponentName(customObject)
	if err != nil {
		return getTypeError(client.ObjectKeyFromObject(customObject).String())
	}
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.nativeClient = mgr.GetClient()
	if err = r.applyOptions(opts...); err != nil {
		return err
	}

	return nil
}

// Reconcile is the entry point from the controller-runtime framework.
// It performs a reconciliation based on the passed ctrl.Request object.
func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// TODO(user): your logic here

	// check if Sample resource exists
	objectInstance, ok := r.prototype.DeepCopyObject().(types.CustomObject)
	if !ok {
		return ctrl.Result{}, getTypeError(req.String())
	}

	if err := r.nativeClient.Get(ctx, req.NamespacedName, objectInstance); err != nil {
		logger.Info(req.NamespacedName.String() + " got deleted!")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// check if deletionTimestamp is set, retry until it gets fully deleted
	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !objectInstance.GetDeletionTimestamp().IsZero() &&
		status.State != types.StateDeleting {
		// if the status is not yet set to deleting, also update the status
		return ctrl.Result{}, r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateDeleting))
	}

	// add finalizer
	if r.options.isFinalizerSet() && controllerutil.AddFinalizer(objectInstance, r.options.finalizer) {
		return ctrl.Result{}, r.nativeClient.Update(ctx, objectInstance)
	}

	switch status.State {
	case "":
		return ctrl.Result{}, r.HandleInitialState(ctx, objectInstance)
	case types.StateProcessing:
		return ctrl.Result{Requeue: true}, r.HandleProcessingState(ctx, objectInstance)
	case types.StateDeleting:
		return ctrl.Result{Requeue: true}, r.HandleDeletingState(ctx, objectInstance)
	case types.StateError:
		return ctrl.Result{Requeue: true}, r.HandleProcessingState(ctx, objectInstance)
	case types.StateReady:
		return ctrl.Result{RequeueAfter: requeueInterval}, r.HandleReadyState(ctx, objectInstance)
	}

	return ctrl.Result{}, nil
}

// HandleInitialState bootstraps state handling for the reconciled resource.
func (r *ManifestReconciler) HandleInitialState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	// set resource labels
	if r.applyLabels(objectInstance) {
		return r.nativeClient.Update(ctx, objectInstance)
	}

	return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateProcessing))
}

func (r *ManifestReconciler) applyLabels(objectInstance types.BaseCustomObject) bool {
	labels := objectInstance.GetLabels()
	updateRequired := false
	if len(r.options.resourceLabels) == 0 {
		return false
	}
	if labels == nil {
		labels = make(map[string]string, 0)
		updateRequired = true
	}

	for key, value := range r.options.resourceLabels {
		if labels[key] == "" {
			labels[key] = value
			updateRequired = true
		}
	}

	if updateRequired {
		objectInstance.SetLabels(labels)
	}
	return updateRequired
}

// HandleProcessingState processes the reconciled resource by processing the underlying resources.
// Based on the processing either a success or failure state is set on the reconciled resource.
func (r *ManifestReconciler) HandleProcessingState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	// TODO: processing logic here
	logger := log.FromContext(ctx)

	// fetch install information
	installSpec, err := r.options.manifestResolver.Get(objectInstance, logger)
	if err != nil {
		return err
	}
	if installSpec.ChartPath == "" {
		return fmt.Errorf("no chart path available for processing")
	}

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	// Use manifest library client to install a sample chart
	installInfo, err := r.prepareInstallInfo(ctx, objectInstance, installSpec,
		resolveReleaseName(installSpec.ReleaseName, objectInstance))
	if err != nil {
		return err
	}

	ready, err := manifest.InstallChart(&logger, installInfo, r.options.objectTransforms, nil)
	if err != nil {
		logger.Error(nil, fmt.Sprintf("error while installing resource %s %s",
			client.ObjectKeyFromObject(objectInstance), err.Error()))
		return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateError))
	}
	if ready {
		return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateReady))
	}
	return nil
}

// HandleDeletingState processed the deletion on the reconciled resource.
// Once the deletion if processed the relevant finalizers (if applied) are removed.
func (r *ManifestReconciler) HandleDeletingState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	logger := log.FromContext(ctx)

	// fetch uninstall information
	installSpec, err := r.options.manifestResolver.Get(objectInstance, logger)
	if err != nil {
		return err
	}
	if installSpec.ChartPath == "" {
		return fmt.Errorf("no chart path available for processing")
	}

	// fallback logic for flags
	if installSpec.SetFlags == nil {
		installSpec.SetFlags = map[string]interface{}{}
	}
	if installSpec.ConfigFlags == nil {
		installSpec.ConfigFlags = map[string]interface{}{}
	}

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	// Use manifest library client to install a sample chart
	installInfo, err := r.prepareInstallInfo(ctx, objectInstance, installSpec,
		resolveReleaseName(installSpec.ReleaseName, objectInstance))
	if err != nil {
		return err
	}

	readyToBeDeleted, err := manifest.UninstallChart(&logger, installInfo, r.options.objectTransforms, nil)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while deleting resource %s", client.ObjectKeyFromObject(objectInstance)))
		status.State = types.StateError
		return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateError))
	}
	// if resources are ready to be deleted, remove finalizer
	if readyToBeDeleted && r.options.isFinalizerSet() &&
		controllerutil.RemoveFinalizer(objectInstance, r.options.finalizer) {
		return r.nativeClient.Update(ctx, objectInstance)
	}
	return nil
}

// HandleReadyState checks for the consistency of reconciled resource, by verifying the underlying resources.
func (r *ManifestReconciler) HandleReadyState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	logger := log.FromContext(ctx)
	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	// fetch install information
	installSpec, err := r.options.manifestResolver.Get(objectInstance, logger)
	if err != nil {
		return err
	}
	if installSpec.ChartPath == "" {
		return fmt.Errorf("no chart path available for processing")
	}

	// Use manifest library client to install a sample chart
	installInfo, err := r.prepareInstallInfo(ctx, objectInstance, installSpec,
		resolveReleaseName(installSpec.ReleaseName, objectInstance))
	if err != nil {
		return err
	}

	// verify installed resources
	ready, err := manifest.ConsistencyCheck(&logger, installInfo, r.options.objectTransforms, nil)

	// update only if resources not ready OR an error occurred during chart verification
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while installing resource %s",
			client.ObjectKeyFromObject(objectInstance)))
		return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateError))
	} else if !ready {
		return r.setStatusForObjectInstance(ctx, objectInstance, status.WithState(types.StateProcessing))
	}
	return nil
}

func (r *ManifestReconciler) prepareInstallInfo(ctx context.Context, objectInstance types.BaseCustomObject,
	installSpec types.InstallationSpec, releaseName string,
) (types.InstallInfo, error) {
	unstructuredObj := &unstructured.Unstructured{}
	var err error
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		unstructuredObj.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
		if err != nil {
			return types.InstallInfo{}, err
		}
	case *unstructured.Unstructured:
		unstructuredObj = typedObject
	default:
		return types.InstallInfo{}, getTypeError(client.ObjectKeyFromObject(objectInstance).String())
	}

	return types.InstallInfo{
		Ctx: ctx,
		ChartInfo: &types.ChartInfo{
			ChartPath:   installSpec.ChartPath,
			ReleaseName: releaseName,
			Flags:       installSpec.ChartFlags,
		},
		ClusterInfo: types.ClusterInfo{
			// destination cluster rest config
			Config: r.config,
			// destination cluster rest client
			Client: r.nativeClient,
		},
		ResourceInfo: types.ResourceInfo{
			// base operator resource to be passed for custom checks
			BaseResource: unstructuredObj,
		},
		CheckReadyStates: r.options.verify,
	}, nil
}

func (r *ManifestReconciler) applyOptions(opts ...ReconcilerOption) error {
	params := manifestOptions{
		force:            false,
		verify:           false,
		resourceLabels:   make(map[string]string, 0),
		objectTransforms: []types.ObjectTransform{},
	}

	for _, opt := range opts {
		params = opt(params)
	}

	if params.manifestResolver == nil {
		return fmt.Errorf("no manifest resolver set, reconciliation cannot proceed")
	}

	r.options = params
	return nil
}

func (r *ManifestReconciler) setStatusForObjectInstance(ctx context.Context, objectInstance types.BaseCustomObject,
	status types.Status,
) error {
	var err error
	var unstructStatus map[string]interface{}
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		typedObject.SetStatus(status)
	case *unstructured.Unstructured:
		unstructStatus, err = runtime.DefaultUnstructuredConverter.ToUnstructured(status)
		if err != nil {
			err = fmt.Errorf("unable to convert unstructured to addonStatus: %w", err)
			break
		}

		if err = unstructured.SetNestedMap(typedObject.Object, unstructStatus, "status"); err != nil {
			err = fmt.Errorf("unable to set status in unstructured: %w", err)
		}
	default:
		err = getTypeError(client.ObjectKeyFromObject(objectInstance).String())
	}

	// return intermediate error
	if err != nil {
		return err
	}

	if err = r.nativeClient.Status().Update(ctx, objectInstance); err != nil {
		return fmt.Errorf("error while updating status %s to: %w", status.State, err)
	}
	return nil
}

func getTypeError(namespacedName string) error {
	return fmt.Errorf("invalid custom resource object type for reconciliation %s", namespacedName)
}

func getStatusFromObjectInstance(objectInstance types.BaseCustomObject) (types.Status, error) {
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		return typedObject.GetStatus(), nil
	case *unstructured.Unstructured:
		unstructStatus, _, err := unstructured.NestedMap(typedObject.Object, "status")
		if err != nil {
			return types.Status{}, fmt.Errorf("unable to get status from unstuctured: %w", err)
		}
		var customStatus types.Status
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructStatus, &customStatus)
		if err != nil {
			return customStatus, err
		}

		return customStatus, nil
	default:
		return types.Status{}, getTypeError(client.ObjectKeyFromObject(objectInstance).String())
	}
}

func GetComponentName(objectInstance types.BaseCustomObject) (string, error) {
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		return typedObject.ComponentName(), nil
	case *unstructured.Unstructured:
		return strings.ToLower(typedObject.GetKind()), nil
	default:
		return "", getTypeError(client.ObjectKeyFromObject(objectInstance).String())
	}
}

func resolveReleaseName(releaseName string, objectInstance types.BaseCustomObject) string {
	if releaseName == "" {
		return objectInstance.GetName()
	}
	return releaseName
}
