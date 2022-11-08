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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"

	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/go-logr/logr"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/internal/pkg/prepare"
	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"
	internalUtil "github.com/kyma-project/module-manager/operator/internal/pkg/util"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
	listener "github.com/kyma-project/runtime-watcher/listener/pkg/event"
)

type RequeueIntervals struct {
	Success time.Duration
}

type OperationRequest struct {
	Info         types.InstallInfo
	Mode         types.Mode
	ResponseChan types.ResponseChan
}

// ManifestReconciler reconciles a Manifest object.
type ManifestReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RestConfig       *rest.Config
	RestMapper       *restmapper.DeferredDiscoveryRESTMapper
	DeployChan       chan OperationRequest
	Workers          *ManifestWorkerPool
	RequeueIntervals RequeueIntervals
	CacheManager     *CacheManager
	internalTypes.ReconcileFlagConfig
}

//+kubebuilder:rbac:groups=operator.kyma-project.io,resources=manifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.kyma-project.io,resources=manifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.kyma-project.io,resources=manifests/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch;get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciliation loop starting for", "resource", req.NamespacedName.String())

	// get manifest object
	manifestObj := v1alpha1.Manifest{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, &manifestObj); err != nil {
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		logger.Info(fmt.Sprintf("%s got deleted", req.NamespacedName.String()))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	manifestObj = *manifestObj.DeepCopy()

	// check if deletionTimestamp is set, retry until it gets fully deleted
	if !manifestObj.DeletionTimestamp.IsZero() && manifestObj.Status.State != v1alpha1.ManifestStateDeleting {
		// if the status is not yet set to deleting, also update the status
		return ctrl.Result{}, r.updateManifestStatus(ctx, &manifestObj, v1alpha1.ManifestStateDeleting,
			"deletion timestamp set")
	}

	// check finalizer on native object
	if !controllerutil.ContainsFinalizer(&manifestObj, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(&manifestObj, labels.ManifestFinalizer)
		return ctrl.Result{}, r.updateManifest(ctx, &manifestObj)
	}

	// state handling
	switch manifestObj.Status.State {
	case "":
		return ctrl.Result{}, r.HandleInitialState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateProcessing:
		return ctrl.Result{},
			r.HandleProcessingState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateDeleting:
		return ctrl.Result{}, r.HandleDeletingState(ctx, &logger, &manifestObj)
	case v1alpha1.ManifestStateError:
		return ctrl.Result{},
			r.HandleErrorState(ctx, &manifestObj)
	case v1alpha1.ManifestStateReady:
		return ctrl.Result{RequeueAfter: r.RequeueIntervals.Success},
			r.HandleReadyState(ctx, &logger, &manifestObj)
	}

	// should not be reconciled again
	return ctrl.Result{}, nil
}

func (r *ManifestReconciler) HandleInitialState(ctx context.Context, _ *logr.Logger, manifestObj *v1alpha1.Manifest,
) error {
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "initial state")
}

func (r *ManifestReconciler) HandleProcessingState(ctx context.Context, logger *logr.Logger,
	manifestObj *v1alpha1.Manifest,
) error {
	return r.sendJobToInstallChannel(ctx, logger, manifestObj, types.CreateMode)
}

func (r *ManifestReconciler) HandleDeletingState(ctx context.Context, logger *logr.Logger,
	manifestObj *v1alpha1.Manifest,
) error {
	return r.sendJobToInstallChannel(ctx, logger, manifestObj, types.DeletionMode)
}

func (r *ManifestReconciler) sendJobToInstallChannel(ctx context.Context, logger *logr.Logger,
	manifestObj *v1alpha1.Manifest, mode types.Mode,
) error {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	responseChan := make(types.ResponseChan)

	chartCount := len(manifestObj.Spec.Installs)

	// response handler in a separate go-routine
	go r.ResponseHandlerFunc(ctx, logger, chartCount, responseChan, namespacedName)

	// send deploy requests
	deployInfos, err := prepare.GetInstallInfos(ctx, manifestObj, types.ClusterInfo{
		Client: r.Client, Config: r.RestConfig,
	}, r.ReconcileFlagConfig, r.CacheManager.ClusterInfos)
	if err != nil {
		logger.Error(err, fmt.Sprintf("cannot prepare install information for %s resource %s",
			v1alpha1.ManifestKind, namespacedName))
		if mode == types.DeletionMode {
			// when installation info cannot not be determined in deletion mode
			// reconciling this resource again will not fix itself
			// so remove finalizer in this case, to process with Manifest deletion
			return r.finalizeDeletion(ctx, manifestObj)
		}
		if err := r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateError, err.Error()); err != nil {
			return err
		}
		return err
	}

	// send processing requests (installation / uninstallation) to deployment channel
	// each individual request will be processed by the next available worker
	for _, deployInfo := range deployInfos {
		r.DeployChan <- OperationRequest{
			Info:         deployInfo,
			Mode:         mode,
			ResponseChan: responseChan,
		}
	}
	return nil
}

func (r *ManifestReconciler) HandleErrorState(ctx context.Context, manifestObj *v1alpha1.Manifest) error {
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing,
		"observed generation change")
}

func (r *ManifestReconciler) HandleReadyState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest,
) error {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	if manifestObj.IsSpecUpdated() {
		logger.Info("observed generation change for " + namespacedName.String())
		return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing,
			"observed generation change")
	}

	logger.V(1).Info("checking consistent state for " + namespacedName.String())

	// send deploy requests
	deployInfos, err := prepare.GetInstallInfos(ctx, manifestObj, types.ClusterInfo{
		Client: r.Client, Config: r.RestConfig,
	}, r.ReconcileFlagConfig, r.CacheManager.ClusterInfos)
	if err != nil {
		return err
	}

	for _, deployInfo := range deployInfos {
		ready, err := manifest.ConsistencyCheck(logger, deployInfo, []types.ObjectTransform{}, r.CacheManager.RenderSources)

		// prepare chart response object
		chartResponse := &types.InstallResponse{
			Ready:             ready,
			ResNamespacedName: client.ObjectKeyFromObject(manifestObj),
			Err:               err,
			ChartName:         deployInfo.ChartName,
			Flags:             deployInfo.Flags,
		}

		// update only if resources not ready OR an error occurred during chart verification
		if !ready {
			internalUtil.AddReadyConditionForResponses([]*types.InstallResponse{chartResponse}, logger, manifestObj)
			return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing,
				"resources not ready")
		} else if err != nil {
			logger.Error(err, fmt.Sprintf("error while performing consistency check on manifest %s", namespacedName))
			internalUtil.AddReadyConditionForResponses([]*types.InstallResponse{chartResponse}, logger, manifestObj)
			if err := r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateError, err.Error()); err != nil {
				return err
			}
			return err
		}
	}
	return nil
}

func (r *ManifestReconciler) updateManifest(ctx context.Context, manifestObj *v1alpha1.Manifest) error {
	return r.Update(ctx, manifestObj)
}

func (r *ManifestReconciler) updateManifestStatus(ctx context.Context, manifestObj *v1alpha1.Manifest,
	state v1alpha1.ManifestState, message string,
) error {
	manifestObj.Status.State = state
	switch state {
	case v1alpha1.ManifestStateReady:
		internalUtil.AddReadyConditionForObjects(manifestObj, []v1alpha1.InstallItem{{ChartName: v1alpha1.ManifestKind}},
			v1alpha1.ConditionStatusTrue, message)
	case "":
		internalUtil.AddReadyConditionForObjects(manifestObj, []v1alpha1.InstallItem{{ChartName: v1alpha1.ManifestKind}},
			v1alpha1.ConditionStatusUnknown, message)
	case v1alpha1.ManifestStateError,
		v1alpha1.ManifestStateDeleting,
		v1alpha1.ManifestStateProcessing:
		internalUtil.AddReadyConditionForObjects(manifestObj, []v1alpha1.InstallItem{{ChartName: v1alpha1.ManifestKind}},
			v1alpha1.ConditionStatusFalse, message)
	}
	return r.Status().Update(ctx, manifestObj.SetObservedGeneration())
}

func (r *ManifestReconciler) HandleCharts(deployInfo types.InstallInfo, mode types.Mode,
	logger *logr.Logger,
) *types.InstallResponse {
	// evaluate create or delete chart
	create := mode == types.CreateMode

	var ready bool
	var err error
	if create {
		ready, err = manifest.InstallChart(logger, deployInfo, []types.ObjectTransform{}, r.CacheManager.RenderSources)
	} else {
		ready, err = manifest.UninstallChart(logger, deployInfo, []types.ObjectTransform{}, r.CacheManager.RenderSources)
	}

	return &types.InstallResponse{
		Ready:             ready,
		ResNamespacedName: client.ObjectKeyFromObject(deployInfo.BaseResource),
		Err:               err,
		ChartName:         deployInfo.ChartName,
		Flags:             deployInfo.Flags,
	}
}

func (r *ManifestReconciler) ResponseHandlerFunc(ctx context.Context, logger *logr.Logger, chartCount int,
	responseChan types.ResponseChan, namespacedName client.ObjectKey,
) {
	// errorState takes precedence over processing
	errorState := false
	processing := false
	// pathError indicates an unfixable error
	// a true value signifies finalizer removal
	pathError := false
	responses := make([]*types.InstallResponse, 0)

	for a := 1; a <= chartCount; a++ {
		select {
		case <-ctx.Done():
			logger.Error(ctx.Err(), fmt.Sprintf("context closed, error occurred while handling response for %s",
				namespacedName.String()))
			return
		case response := <-responseChan:
			responses = append(responses, response)
			if response.Err != nil {
				// if there is a local path error, we assume that it's an error in CR creation itself
				// so this should not be marked in error state
				// as this will hinder deletion
				var pathErr *fs.PathError
				pathError = errors.As(response.Err, &pathErr)
				logger.Error(fmt.Errorf("chart installation failure for '%s': %w",
					response.ResNamespacedName.String(), response.Err), "")
				errorState = true
			} else if !response.Ready {
				logger.Info(fmt.Sprintf("chart checks still processing '%s'",
					response.ResNamespacedName.String()))
				processing = true
			}
		}
	}

	latestManifestObj := &v1alpha1.Manifest{}
	if err := r.Get(ctx, namespacedName, latestManifestObj); err != nil {
		// if manifest CR is not found at this point return with no errors,
		// as finalizer could be removed by a parallel worker
		if apierrors.IsNotFound(err) {
			return
		}
		logger.V(util.DebugLogLevel).Error(err, "error while locating", "resource", namespacedName)
		return
	}

	internalUtil.AddReadyConditionForResponses(responses, logger, latestManifestObj)

	// handle deletion if no previous error occurred
	if (!errorState || pathError) &&
		!latestManifestObj.DeletionTimestamp.IsZero() &&
		!processing {
		err := r.finalizeDeletion(ctx, latestManifestObj)
		if err == nil {
			return
		}

		// finalizer removal failure - set error state
		logger.V(util.DebugLogLevel).Error(err, "unexpected error while removing finalizer from",
			"resource", namespacedName)
		errorState = true
	}

	r.setProcessedState(ctx, errorState, processing, latestManifestObj, logger)
}

func (r *ManifestReconciler) setProcessedState(ctx context.Context, errorState bool, processing bool,
	manifestObj *v1alpha1.Manifest, logger *logr.Logger,
) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	endState := v1alpha1.ManifestStateDeleting
	if errorState {
		endState = v1alpha1.ManifestStateError
	} else if manifestObj.DeletionTimestamp.IsZero() {
		// only update to processing, ready if deletion has not been triggered
		if processing {
			endState = v1alpha1.ManifestStateProcessing
		} else {
			endState = v1alpha1.ManifestStateReady
		}
	}

	// update status for non-deletion scenarios
	if err := r.updateManifestStatus(ctx, manifestObj, endState,
		fmt.Sprintf("%s in %s state", v1alpha1.ManifestKind, endState)); err != nil {
		logger.Error(err, "error updating status", "resource", namespacedName)
	}
}

func ManifestRateLimiter(failureBaseDelay time.Duration, failureMaxDelay time.Duration,
	frequency int, burst int,
) ratelimiter.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(failureBaseDelay, failureMaxDelay),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(frequency), burst)})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager,
	failureBaseDelay time.Duration, failureMaxDelay time.Duration, frequency int, burst int, listenerAddr string,
) error {
	r.DeployChan = make(chan OperationRequest, r.Workers.GetWorkerPoolSize())
	r.Workers.StartWorkers(ctx, r.DeployChan, r.HandleCharts)

	// default config from kubebuilder
	r.RestConfig = mgr.GetConfig()

	// initialize new cluster cache
	r.CacheManager = NewCacheManager()

	// register listener component
	runnableListener, eventChannel := listener.RegisterListenerComponent(
		listenerAddr, strings.ToLower(labels.OperatorName))

	// start listener as a manager runnable
	if err := mgr.Add(runnableListener); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Manifest{}).
		Watches(&source.Kind{Type: &v1.Secret{}}, handler.Funcs{}).
		Watches(eventChannel, &handler.Funcs{
			GenericFunc: func(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
				ctrl.Log.WithName("listener").Info(
					fmt.Sprintf("event coming from SKR, adding %s to queue",
						client.ObjectKeyFromObject(event.Object).String()),
				)

				queue.Add(ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(event.Object),
				})
			},
		}).
		WithOptions(controller.Options{
			RateLimiter:             ManifestRateLimiter(failureBaseDelay, failureMaxDelay, frequency, burst),
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		Complete(r)
}

func (r *ManifestReconciler) finalizeDeletion(ctx context.Context, manifestObj *v1alpha1.Manifest) error {
	// remove finalizer
	finalizerRemoved := controllerutil.RemoveFinalizer(manifestObj, labels.ManifestFinalizer)

	// finally update Manifest, if finalizer was removed
	if !finalizerRemoved {
		return nil
	}

	kymaOwnerLabel, err := util.GetResourceLabel(manifestObj, labels.ComponentOwner)
	if err != nil {
		return err
	}

	manifestList := &v1alpha1.ManifestList{}
	if err != nil {
		return err
	}
	err = r.Client.List(ctx, manifestList, &client.ListOptions{
		LabelSelector: k8slabels.SelectorFromSet(k8slabels.Set{labels.ComponentOwner: kymaOwnerLabel}),
		Namespace:     manifestObj.Namespace,
	})
	if err != nil {
		return err
	}
	// delete cluster cache entry only if the Manifest being deleted is the only one
	// with the corresponding kyma name
	if len(manifestList.Items) == 1 {
		r.CacheManager.Invalidate(client.ObjectKey{Name: kymaOwnerLabel, Namespace: manifestObj.Namespace})
	}

	return r.updateManifest(ctx, manifestObj)
}
