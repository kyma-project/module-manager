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
	"helm.sh/helm/v3/pkg/cli"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)

const (
	DefaultWorkersCount               = 4
	WaitTimeout         time.Duration = 5 * time.Minute
)

type RequestErrChan chan *RequestError

type DeployInfo struct {
	v1alpha1.ChartInfo
	Mode
	client.ObjectKey
	RequestErrChan
	RestConfig *rest.Config
}

// ManifestReconciler reconciles a Manifest object
type ManifestReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	RestConfig *rest.Config
	RestMapper *restmapper.DeferredDiscoveryRESTMapper
	DeployChan chan DeployInfo
	Workers    *ManifestWorkers
}

type RequestError struct {
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *RequestError) Error() string {
	return r.Err.Error()
}

//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests/custom,verbs=get;update;patch
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
		// if the custom is not yet set to deleting, also update the custom
		return ctrl.Result{}, r.updateManifestStatus(ctx, &manifestObj, v1alpha1.ManifestStateDeleting, "deletion timestamp set")
	}

	// check finalizer
	if !controllerutil.ContainsFinalizer(&manifestObj, manifestFinalizer) {
		controllerutil.AddFinalizer(&manifestObj, manifestFinalizer)
		return ctrl.Result{}, r.updateManifest(ctx, &manifestObj)
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
		return ctrl.Result{}, r.HandleReadyState(ctx, &logger, &manifestObj)
	}

	// should not be reconciled again
	return ctrl.Result{}, nil
}

func (r *ManifestReconciler) HandleInitialState(ctx context.Context, _ *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "initial state")
}

func (r *ManifestReconciler) HandleProcessingState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.jobAllocator(ctx, logger, manifestObj, CreateMode)
}

func (r *ManifestReconciler) HandleDeletingState(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest) error {
	return r.jobAllocator(ctx, logger, manifestObj, DeletionMode)
}

func (r *ManifestReconciler) jobAllocator(ctx context.Context, logger *logr.Logger, manifestObj *v1alpha1.Manifest, mode Mode) error {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	responseChan := make(RequestErrChan)
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

	// send job to workers
	for _, chart := range manifestObj.Spec.Charts {
		r.DeployChan <- DeployInfo{
			ChartInfo:      chart,
			Mode:           mode,
			ObjectKey:      namespacedName,
			RequestErrChan: responseChan,
			RestConfig:     restConfig,
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
	if manifestObj.Status.ObservedGeneration == manifestObj.Generation {
		logger.Info("skipping reconciliation for " + manifestObj.Name + ", already reconciled!")
		return nil
	}
	return r.updateManifestStatus(ctx, manifestObj, v1alpha1.ManifestStateProcessing, "observed generation change")
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

func (r *ManifestReconciler) HandleCharts(deployInfo DeployInfo, logger *logr.Logger) *RequestError {
	var (
		args = map[string]string{
			// check --set flags parameter from manifest
			"set": "",
			// comma seperated values of manifest command line flags
			"flags": deployInfo.ClientConfig,
		}
		repoName    = deployInfo.RepoName
		url         = deployInfo.Url
		chartName   = deployInfo.ChartName
		releaseName = deployInfo.ReleaseName
	)

	// evaluate create or delete chart
	create := deployInfo.Mode == CreateMode

	// TODO: implement better settings handling
	manifestOperations := manifest.NewOperations(logger, deployInfo.RestConfig, cli.New(), WaitTimeout)
	var err error

	if create {
		err = manifestOperations.Install("", releaseName, fmt.Sprintf("%s/%s", repoName, chartName), repoName, url, args)
	} else {
		err = manifestOperations.Uninstall("", fmt.Sprintf("%s/%s", repoName, chartName), releaseName, args)
	}

	return &RequestError{
		ResNamespacedName: deployInfo.ObjectKey,
		Err:               err,
	}
}

func (r *ManifestReconciler) ResponseHandlerFunc(ctx context.Context, logger *logr.Logger, chartCount int, responseChan RequestErrChan, namespacedName client.ObjectKey) {
	errorState := false
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
			}
		}
	}

	latestManifestObj := &v1alpha1.Manifest{}
	if err := r.Get(ctx, namespacedName, latestManifestObj); err != nil {
		logger.Error(err, "error while locating", "resource", namespacedName)
		return
	}

	// handle deletion if no previous error occurred
	if !errorState && !latestManifestObj.DeletionTimestamp.IsZero() {
		// remove finalizer
		controllerutil.RemoveFinalizer(latestManifestObj, manifestFinalizer)
		if err := r.updateManifest(ctx, latestManifestObj); err != nil {
			// finalizer removal failure
			logger.Error(err, "unexpected error while deleting", "resource", namespacedName)
			errorState = true
		} else {
			// finalizer successfully removed
			return
		}
	}

	var customResourcesReady bool
	if !errorState {
		customResourcesReady, errorState = r.AreCustomResourcesReady(ctx, latestManifestObj.Labels, namespacedName, logger)
	}

	var endState v1alpha1.ManifestState
	if errorState {
		endState = v1alpha1.ManifestStateError
	} else if !customResourcesReady {
		endState = v1alpha1.ManifestStateProcessing
	} else {
		endState = v1alpha1.ManifestStateReady
	}

	// update custom for non-deletion scenarios
	if err := r.updateManifestStatus(ctx, latestManifestObj, endState, "manifest charts installed!"); err != nil {
		logger.Error(err, "error updating custom", "resource", namespacedName)
	}
	return
}

func (r *ManifestReconciler) AreCustomResourcesReady(ctx context.Context, kymaOwnerLabels map[string]string, namespacedName client.ObjectKey, logger *logr.Logger) (bool, bool) {
	kymaOwnerLabel, ok := kymaOwnerLabels[labels.ComponentOwner]
	if !ok {
		logger.Error(fmt.Errorf("label %s not set for manifest resource %s", labels.ComponentOwner, namespacedName), "")
		return false, true
	}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: r.Client}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, namespacedName.Namespace)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while evaluating rest config for manifest resource %s", namespacedName))
		return false, true
	}

	customClient, err := clusterClient.GetNewClient(restConfig)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while evaluating target client for manifest resource %s", namespacedName))
		return false, true
	}

	// check custom resource for custom
	customStatus := &custom.CustomStatus{
		Reader: customClient,
	}

	ready, err := customStatus.WaitForCustomResources(ctx, []custom.CustomWaitResource{
		{
			GroupVersionKind: schema.GroupVersionKind{
				Group:   "operator.kyma-project.io",
				Version: "v1alpha1",
				Kind:    "Kyma",
			},
			ObjectKey: client.ObjectKey{
				Name:      "kyma-sample",
				Namespace: "default",
			},
			ResStatus: "Processing",
		},
	})
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while tracking custom of custom resources for manifest resource %s", namespacedName))
		return false, true
	}

	return ready, false
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.DeployChan = make(chan DeployInfo)
	r.Workers.StartWorkers(ctx, r.DeployChan, r.HandleCharts)

	// default config from kubebuilder
	//r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Manifest{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: DefaultWorkersCount,
		}).
		Complete(r)
}
