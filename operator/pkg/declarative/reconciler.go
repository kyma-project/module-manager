package declarative

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/strvals"
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
)

var _ reconcile.Reconciler = &ManifestReconciler{}

const (
	deletionFinalizer = "custom-deletion-finalizer"
	requeueInterval   = time.Second * 3
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
		status.State = types.StateDeleting
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.nativeClient.Status().Update(ctx, objectInstance)
	}

	// add deletion finalizer
	if controllerutil.AddFinalizer(objectInstance, deletionFinalizer) {
		return ctrl.Result{}, r.nativeClient.Update(ctx, objectInstance)
	}

	switch status.State {
	case "":
		return ctrl.Result{}, r.HandleInitialState(ctx, objectInstance)
	case types.StateProcessing:
		return ctrl.Result{RequeueAfter: requeueInterval}, r.HandleProcessingState(ctx, objectInstance)
	case types.StateDeleting:
		return ctrl.Result{}, r.HandleDeletingState(ctx, objectInstance)
	case types.StateError:
		return ctrl.Result{RequeueAfter: requeueInterval}, r.HandleErrorState(ctx, objectInstance)
	case types.StateReady:
		return ctrl.Result{RequeueAfter: requeueInterval}, r.HandleReadyState(ctx, objectInstance)
	}

	return ctrl.Result{}, nil
}

func (r *ManifestReconciler) HandleInitialState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	// TODO: initial logic here

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	// set resource labels
	if r.applyLabels(objectInstance) {
		return r.nativeClient.Update(ctx, objectInstance)
	}

	// Example: Set to Processing state
	status.State = types.StateProcessing
	if err = setStatusForObjectInstance(objectInstance, status); err != nil {
		return err
	}
	return r.nativeClient.Status().Update(ctx, objectInstance)
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

func (r *ManifestReconciler) HandleProcessingState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	// TODO: processing logic here
	logger := log.FromContext(ctx)
	spec, err := getSpecFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	manifestClient, err := r.getManifestClient(&logger, spec.ChartFlags)
	if err != nil {
		logger.Error(err, "error while parsing flags for resource %s", client.ObjectKeyFromObject(objectInstance))
		status.State = types.StateError
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return err
		}
		return r.nativeClient.Status().Update(ctx, objectInstance)
	}

	// Use manifest library client to install a sample chart
	installInfo, err := r.prepareInstallInfo(ctx, objectInstance, spec.ChartPath, spec.ReleaseName)
	if err != nil {
		return err
	}

	ready, err := manifestClient.Install(installInfo)
	if err != nil {
		logger.Error(err, "error while installing resource %s", client.ObjectKeyFromObject(objectInstance))
		status.State = types.StateError
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return err
		}
		return r.nativeClient.Status().Update(ctx, objectInstance)
	}
	if ready {
		status.State = types.StateReady
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return err
		}
		return r.nativeClient.Status().Update(ctx, objectInstance)
	}
	return nil
}

func (r *ManifestReconciler) HandleDeletingState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	// TODO: deletion logic here
	logger := log.FromContext(ctx)
	spec, err := getSpecFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}

	manifestClient, err := r.getManifestClient(&logger, spec.ChartFlags)
	if err != nil {
		logger.Error(err, "error while parsing flags for resource %s", client.ObjectKeyFromObject(objectInstance))
		status.State = types.StateError
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return err
		}
		return r.nativeClient.Status().Update(ctx, objectInstance)
	}

	// Use manifest library client to install a sample chart
	installInfo, err := r.prepareInstallInfo(ctx, objectInstance, spec.ChartPath, spec.ReleaseName)
	if err != nil {
		return err
	}

	readyToBeDeleted, err := manifestClient.Uninstall(installInfo)
	if err != nil {
		logger.Error(err, "error while deleting resource %s", client.ObjectKeyFromObject(objectInstance))
		status.State = types.StateError
		if err = setStatusForObjectInstance(objectInstance, status); err != nil {
			return err
		}
		return r.nativeClient.Status().Update(ctx, objectInstance)
	}
	if readyToBeDeleted {
		// Example: If Deleting state, remove Finalizers
		if controllerutil.RemoveFinalizer(objectInstance, deletionFinalizer) {
			return r.nativeClient.Update(ctx, objectInstance)
		}
	}
	return nil
}

func (r *ManifestReconciler) HandleErrorState(ctx context.Context, objectInstance types.BaseCustomObject) error {
	// TODO: error logic here

	status, err := getStatusFromObjectInstance(objectInstance)
	if err != nil {
		return err
	}
	// Example: If Error state, set state to Processing
	status.State = types.StateProcessing
	if err = setStatusForObjectInstance(objectInstance, status); err != nil {
		return err
	}
	return r.nativeClient.Status().Update(ctx, objectInstance)
}

func (r *ManifestReconciler) HandleReadyState(_ context.Context, _ types.BaseCustomObject) error {
	// TODO: ready logic here

	// Example: If Ready state, check consistency of deployed module
	return nil
}

func (r *ManifestReconciler) prepareInstallInfo(ctx context.Context, objectInstance types.BaseCustomObject,
	chartPath string, releaseName string,
) (manifest.InstallInfo, error) {
	unstructuredObj := &unstructured.Unstructured{}
	var err error
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		unstructuredObj.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
		if err != nil {
			return manifest.InstallInfo{}, err
		}
	case *unstructured.Unstructured:
		unstructuredObj = typedObject
	default:
		return manifest.InstallInfo{}, getTypeError(client.ObjectKeyFromObject(objectInstance).String())
	}

	return manifest.InstallInfo{
		Ctx: ctx,
		ChartInfo: &manifest.ChartInfo{
			ChartPath:   chartPath,
			ReleaseName: releaseName,
		},
		RemoteInfo: custom.RemoteInfo{
			// destination cluster rest config
			RemoteConfig: r.config,
			// destination cluster rest client
			RemoteClient: &r.nativeClient,
		},
		ResourceInfo: manifest.ResourceInfo{
			// base operator resource to be passed for custom checks
			BaseResource: unstructuredObj,
		},
		CheckFn: func(context.Context, *unstructured.Unstructured, *logr.Logger, custom.RemoteInfo) (bool, error) {
			// your custom logic here to set ready state
			return true, nil
		},
		CheckReadyStates: true,
	}, nil
}

func (r *ManifestReconciler) getManifestClient(logger *logr.Logger, configString string,
) (*manifest.Operations, error) {
	config := map[string]interface{}{}
	if err := strvals.ParseInto(configString, config); err != nil {
		return nil, err
	}
	// Example: Prepare manifest library client
	return manifest.NewOperations(logger, r.config, "nginx-release-name", cli.New(),
		map[string]map[string]interface{}{
			// check --set flags parameter for helm
			"set": {},
			// comma separated values of manifest command line flags
			"flags": config,
		}, r.options.objectTransforms)
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

	r.options = params
	return nil
}

func setStatusForObjectInstance(objectInstance types.BaseCustomObject, status types.Status) error {
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		typedObject.SetStatus(status)
	case *unstructured.Unstructured:
		unstructStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(status)
		if err != nil {
			return fmt.Errorf("unable to convert unstructured to addonStatus: %w", err)
		}

		err = unstructured.SetNestedMap(typedObject.Object, unstructStatus, "status")
		if err != nil {
			return fmt.Errorf("unable to set status in unstructured: %w", err)
		}

		return nil
	default:
		return getTypeError(client.ObjectKeyFromObject(objectInstance).String())
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

func getSpecFromObjectInstance(objectInstance types.BaseCustomObject) (types.Spec, error) {
	switch typedObject := objectInstance.(type) {
	case types.CustomObject:
		return typedObject.GetSpec(), nil
	case *unstructured.Unstructured:
		unstructSpec, _, err := unstructured.NestedMap(typedObject.Object, "spec")
		if err != nil {
			return types.Spec{}, fmt.Errorf("unable to get spec from unstuctured: %w", err)
		}
		var addonSpec types.Spec
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructSpec, &addonSpec)
		if err != nil {
			return addonSpec, err
		}

		return addonSpec, nil
	default:
		return types.Spec{}, getTypeError(client.ObjectKeyFromObject(objectInstance).String())
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
