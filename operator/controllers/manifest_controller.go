/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	"golang.org/x/time/rate"
	"helm.sh/helm/v3/pkg/cli"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
)

const (
	DefaultWorkersCount = 4
	ReadyCheck          = false
)

type ManifestDeploy struct {
	Info           manifest.DeployInfo
	Mode           manifest.Mode
	RequestErrChan manifest.RequestErrChan
}

// ManifestReconciler reconciles a Manifest object
type ManifestReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	RestConfig *rest.Config
	RestMapper *restmapper.DeferredDiscoveryRESTMapper
	DeployChan chan ManifestDeploy
	Workers    *ManifestWorkers
}

//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName(req.NamespacedName.String())
	logger.Info("Reconciliation loop starting for", "resource", req.NamespacedName.String())

	// get manifest object
	manifestObj := v1alpha1.Manifest{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, &manifestObj); err != nil {
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		logger.Info(req.NamespacedName.String() + " got deleted!")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	manifestObj = *manifestObj.DeepCopy()

	// check if deletionTimestamp is set, retry until it gets fully deleted
	if !manifestObj.DeletionTimestamp.IsZero() && manifestObj.Status.State != v1alpha1.ManifestStateDeleting {
		// if the status is not yet set to deleting, also update the status
		return ctrl.Result{}, r.updateManifestStatus(ctx, &manifestObj, v1alpha1.ManifestStateDeleting, "deletion timestamp set")
	}

	// check finalizer on native object
	if !controllerutil.ContainsFinalizer(&manifestObj, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(&manifestObj, labels.ManifestFinalizer)
		return ctrl.Result{}, r.updateManifest(ctx, &manifestObj)
	}

	// check remote object
	remoteInterface, err := NewRemoteInterface(ctx, r.Client, &manifestObj)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !remoteInterface.IsSynced() {
		return ctrl.Result{}, r.Update(ctx, remoteInterface.NativeObject)
	}

	// state handling
	switch manifestObj.Status.State {
	case "":
		return ctrl.Result{}, r.HandleInitialState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateProcessing:
		return ctrl.Result{}, r.HandleProcessingState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateDeleting:
		return ctrl.Result{}, r.HandleDeletingState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateError:
		return ctrl.Result{}, r.HandleErrorState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateReady:
		return ctrl.Result{RequeueAfter: time.Second * 10}, r.HandleReadyState(ctx, &logger, &manifestObj)
	}

	// should not be reconciled again
	return ctrl.Result{}, nil
}

func (r *ManifestReconciler) HandleInitialState(ctx context.Context, _ *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "initial state")
}

func (r *ManifestReconciler) HandleProcessingState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.jobAllocator(ctx, logger, manifestObj, manifest.CreateMode)
}

func (r *ManifestReconciler) HandleDeletingState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.jobAllocator(ctx, logger, manifestObj, manifest.DeletionMode)
}

func (r *ManifestReconciler) jobAllocator(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest, mode manifest.Mode) error {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	responseChan := make(manifest.RequestErrChan)
	chartCount := len(manifestObj.Spec.Charts)
	kymaOwnerLabel, ok := manifestObj.Labels[labels.ComponentOwner]
	if !ok {
		return fmt.Errorf("label %s not set for manifest resource %s", labels.ComponentOwner, namespacedName)
	}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: r.Client}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace)
	if err != nil {
		return err
	}

	go r.ResponseHandlerFunc(ctx, logger, chartCount, responseChan, namespacedName)

	customResCheck := &CustomResourceCheck{DefaultClient: r.Client}

	// send job to workers
	for _, chart := range manifestObj.Spec.Charts {
		r.DeployChan <- ManifestDeploy{
			Info: manifest.DeployInfo{
				Ctx:            ctx,
				ManifestLabels: manifestObj.Labels,
				ChartInfo:      manifest.ChartInfo(chart),
				ObjectKey:      namespacedName,
				RestConfig:     restConfig,
				CheckFn:        customResCheck.CheckProcessingFn,
				ReadyCheck:     ReadyCheck,
			},
			Mode:           mode,
			RequestErrChan: responseChan,
		}
	}

	return nil
}

func (r *ManifestReconciler) HandleErrorState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	if manifestObj.Status.ObservedGeneration == manifestObj.Generation {
		logger.Info("skipping reconciliation for " + manifestObj.Name + ", already reconciled!")
		return nil
	}
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "observed generation change")
}

func (r *ManifestReconciler) HandleReadyState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	logger.Info("checking consistent state for " + manifestObj.Name)

	namespacedName := client.ObjectKeyFromObject(manifestObj)
	kymaOwnerLabel, ok := manifestObj.Labels[labels.ComponentOwner]
	if !ok {
		return fmt.Errorf("label %s not set for manifest resource %s", labels.ComponentOwner, namespacedName)
	}
	customResCheck := &CustomResourceCheck{DefaultClient: r.Client}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: r.Client}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace)
	if err != nil {
		return err
	}
	for _, chart := range manifestObj.Spec.Charts {
		deployInfo := manifest.DeployInfo{
			Ctx:            ctx,
			ManifestLabels: manifestObj.Labels,
			ChartInfo:      manifest.ChartInfo(chart),
			ObjectKey:      namespacedName,
			RestConfig:     restConfig,
			CheckFn:        customResCheck.CheckReadyFn,
			ReadyCheck:     ReadyCheck,
		}
		args := PrepareArgs(&deployInfo)
		manifestOperations, err := manifest.NewOperations(logger, deployInfo.RestConfig, deployInfo.ReleaseName, cli.New(), args)
		if err != nil {
			logger.Error(err, fmt.Sprintf("error while creating library operations for manifest %s", namespacedName))
			return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateError, err.Error())
		}

		if ready, err := manifestOperations.VerifyResources(deployInfo); !ready {
			return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "resources not ready")
		} else if err != nil {
			logger.Error(err, fmt.Sprintf("error while performing consistency check on manifest %s", namespacedName))
			return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateError, err.Error())
		}
	}
	return nil
}

func (r *ManifestReconciler) updateManifest(ctx context.Context, manifestObj *v1alpha1.Manifest) error {
	return r.Update(ctx, manifestObj)
}

func (r *ManifestReconciler) updateManifestStatus(ctx context.Context, manifestObj *v1alpha1.Manifest, state v1alpha1.ManifestState, message string) error {
	manifestObj.Status.State = state
	switch state {
	case v1alpha1.ManifestStateReady:
		addReadyConditionForObjects(manifestObj, []string{v1alpha1.ManifestKind}, v1alpha1.ConditionStatusTrue, message)
	case "":
		addReadyConditionForObjects(manifestObj, []string{v1alpha1.ManifestKind}, v1alpha1.ConditionStatusUnknown, message)
	default:
		addReadyConditionForObjects(manifestObj, []string{v1alpha1.ManifestKind}, v1alpha1.ConditionStatusFalse, message)
	}
	return r.Status().Update(ctx, manifestObj.SetObservedGeneration())
}

func (r *ManifestReconciler) HandleCharts(deployInfo manifest.DeployInfo, mode manifest.Mode, logger *logr.Logger) *manifest.RequestError {
	args := PrepareArgs(&deployInfo)

	// evaluate create or delete chart
	create := mode == manifest.CreateMode

	var ready bool
	// TODO: implement better settings handling
	manifestOperations, err := manifest.NewOperations(logger, deployInfo.RestConfig, deployInfo.ReleaseName, cli.New(), args)

	if err == nil {
		if create {
			ready, err = manifestOperations.Install(deployInfo)
		} else {
			deployInfo.CheckFn = nil
			ready, err = manifestOperations.Uninstall(deployInfo)
		}
	}

	return &manifest.RequestError{
		Ready:             ready,
		ResNamespacedName: deployInfo.ObjectKey,
		Err:               err,
	}
}

func (r *ManifestReconciler) ResponseHandlerFunc(ctx context.Context, logger *logr.Logger, chartCount int, responseChan manifest.RequestErrChan, namespacedName client.ObjectKey) {
	errorState := false
	processing := false
	for a := 1; a <= chartCount; a++ {
		select {
		case <-ctx.Done():
			logger.Error(ctx.Err(), fmt.Sprintf("context closed, error occured while handling response for %s", namespacedName.String()))
			return
		case response := <-responseChan:
			if response.Err != nil {
				logger.Error(response.Err, fmt.Sprintf("chart installation failure for %s!!!", response.ResNamespacedName.String()))
				errorState = true
				break
			} else if !response.Ready {
				logger.Info(fmt.Sprintf("chart checks still processing %s!!!", response.ResNamespacedName.String()))
				processing = true
				break
			}
		}
	}

	latestManifestObj := &v1alpha1.Manifest{}
	if err := r.Get(ctx, namespacedName, latestManifestObj); err != nil {
		logger.Error(err, "error while locating", "resource", namespacedName)
		return
	}

	// handle deletion if no previous error occurred
	if !errorState && !latestManifestObj.DeletionTimestamp.IsZero() && !processing {
		// remove finalizer
		controllerutil.RemoveFinalizer(latestManifestObj, labels.ManifestFinalizer)
		if err := r.updateManifest(ctx, latestManifestObj); err != nil {
			// finalizer removal failure
			logger.Error(err, "unexpected error while deleting", "resource", namespacedName)
			errorState = true
		} else {
			// finalizer successfully removed
			return
		}
	}

	endState := v1alpha1.ManifestStateDeleting
	if errorState {
		endState = v1alpha1.ManifestStateError
	} else if latestManifestObj.DeletionTimestamp.IsZero() {
		// only update to processing, ready if deletion has not been triggered
		if processing {
			endState = v1alpha1.ManifestStateProcessing
		} else {
			endState = v1alpha1.ManifestStateReady
		}
	}

	// update status for non-deletion scenarios
	if err := r.updateManifestStatus(ctx, latestManifestObj, endState, "manifest charts installed!"); err != nil {
		logger.Error(err, "error updating status", "resource", namespacedName)
	}
	return
}

func ManifestRateLimiter() ratelimiter.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(30), 200)})
}

func PrepareArgs(deployInfo *manifest.DeployInfo) map[string]string {
	var (
		args = map[string]string{
			// check --set flags parameter from manifest
			"set": "",
			// comma seperated values of manifest command line flags
			"flags": deployInfo.ClientConfig,
		}
	)
	deployInfo.ChartName = fmt.Sprintf("%s/%s", deployInfo.RepoName, deployInfo.ChartName)
	return args
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.DeployChan = make(chan ManifestDeploy)
	r.Workers.StartWorkers(ctx, r.DeployChan, r.HandleCharts)

	// default config from kubebuilder
	//r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Manifest{}).
		Watches(&source.Kind{Type: &v1.Secret{}}, handler.Funcs{}).
		WithOptions(controller.Options{
			RateLimiter:             ManifestRateLimiter(),
			MaxConcurrentReconciles: DefaultWorkersCount,
		}).
		Complete(r)
}
