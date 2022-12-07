package v2

import (
	"context"
	"errors"
	"time"

	manifestClient "github.com/kyma-project/module-manager/pkg/client"
	manifestLabels "github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func NewFromManager(mgr manager.Manager, prototype Object, options ...Option) reconcile.Reconciler {
	r := &ManifestReconciler{}
	r.prototype = prototype
	r.Options = DefaultOptions().Apply(WithManager(mgr)).Apply(options...)
	return r
}

type ManifestReconciler struct {
	prototype Object
	*Options
}

type ConditionType string

const (
	ConditionTypeCRDs         ConditionType = "CRDs"
	ConditionTypeResources    ConditionType = "Resources"
	ConditionTypeInstallation ConditionType = "Installation"
)

type ConditionReason string

const (
	ConditionReasonCRDsAreAvailable      ConditionReason = "CRDsAvailable"
	ConditionReasonResourcesAreAvailable ConditionReason = "ResourcesAvailable"
	ConditionReasonReady                 ConditionReason = "Ready"
)

//nolint:gochecknoglobals
var (
	crdCondition = metav1.Condition{
		Type:    string(ConditionTypeCRDs),
		Reason:  string(ConditionReasonCRDsAreAvailable),
		Status:  metav1.ConditionFalse,
		Message: "CustomResourceDefinitions are installed and ready for use",
	}
	resourceCondition = metav1.Condition{
		Type:    string(ConditionTypeResources),
		Reason:  string(ConditionReasonResourcesAreAvailable),
		Status:  metav1.ConditionFalse,
		Message: "resources are parsed and ready for use",
	}
	installationCondition = metav1.Condition{
		Type:    string(ConditionTypeInstallation),
		Reason:  string(ConditionReasonReady),
		Status:  metav1.ConditionFalse,
		Message: "installation is ready and resources can be used",
	}
)

//nolint:funlen,nestif,gocognit,gocyclo,cyclop,maintidx //TODO discuss if worth breaking up or if more readable as is.
func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	obj := r.prototype.DeepCopyObject().(Object)

	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		logger.Info(req.NamespacedName.String() + " got deleted!")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// ServerSideApply requires nil ManagedFields
	obj.SetManagedFields(nil)

	status := obj.GetStatus()

	if !obj.GetDeletionTimestamp().IsZero() && obj.GetStatus().State != StateDeleting {
		obj.SetStatus(status.WithState(StateDeleting).WithOperation("switched to deleting"))
		return r.ssaStatus(ctx, obj)
	}

	// add finalizer
	if controllerutil.AddFinalizer(obj, r.Finalizer) {
		return r.ssa(ctx, obj)
	}

	crdCondition := *crdCondition.DeepCopy()
	resourceCondition := *resourceCondition.DeepCopy()
	installationCondition := *installationCondition.DeepCopy()

	crdCondition.ObservedGeneration = obj.GetGeneration()
	resourceCondition.ObservedGeneration = obj.GetGeneration()
	installationCondition.ObservedGeneration = obj.GetGeneration()

	// Processing
	if status.State == "" {
		presetConditions := 3
		status.Conditions = make([]metav1.Condition, 0, presetConditions)
		meta.SetStatusCondition(&status.Conditions, crdCondition)
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
		meta.SetStatusCondition(&status.Conditions, installationCondition)
		status.Synced = make([]Resource, 0)
		obj.SetStatus(status.WithState(StateProcessing).WithOperation("switched to processing"))
		return r.ssaStatus(ctx, obj)
	}

	spec, err := r.ResolveManifestSpec(ctx, obj)
	if err != nil {
		r.Event(obj, "Warning", "ResolveManifestSpec", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	clients, err := r.initializeClients(ctx, obj, spec)
	if err != nil {
		r.Event(obj, "Warning", "ClientInitialization", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}
	resourceConverter := NewResourceConverter(clients, clients.KubeClient().Namespace)

	var readyCheck ReadyCheck
	if r.CustomReadyCheck != nil {
		readyCheck = r.CustomReadyCheck
	} else {
		readyCheck = NewHelmReadyCheck(clients)
	}

	chrt, err := loader.Load(spec.ChartPath)
	if err != nil {
		r.Event(obj, "Warning", "ChartLoading", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	crds, err := getCRDs(clients, chrt.CRDObjects())
	if err != nil {
		r.Event(obj, "Warning", "CRDParsing", err.Error())
		crdCondition.Status = metav1.ConditionFalse
		meta.SetStatusCondition(&status.Conditions, crdCondition)
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	if obj.GetDeletionTimestamp().IsZero() && !meta.IsStatusConditionTrue(status.Conditions, crdCondition.Type) {
		if err := installCRDs(clients, crds); err != nil {
			r.Event(obj, "Warning", "CRDInstallation", err.Error())
			meta.SetStatusCondition(&status.Conditions, crdCondition)
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return r.ssaStatus(ctx, obj)
		}

		crdsReady, err := readyCheck.Run(ctx, crds)
		if err != nil {
			r.Event(obj, "Warning", "CRDReadyCheck", err.Error())
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return r.ssaStatus(ctx, obj)
		}

		if !crdsReady {
			r.Event(obj, "Normal", "CRDReadyCheck", "crds are not yet ready...")
			return ctrl.Result{Requeue: true}, nil
		}

		restMapper, _ := clients.ToRESTMapper()
		meta.MaybeResetRESTMapper(restMapper)
		r.Event(obj, "Normal", crdCondition.Reason, crdCondition.Message)
		crdCondition.Status = metav1.ConditionTrue
		meta.SetStatusCondition(&status.Conditions, crdCondition)
		obj.SetStatus(status.WithOperation(crdCondition.Message))
		return r.ssaStatus(ctx, obj)
	}

	var target, current kube.ResourceList

	if obj.GetDeletionTimestamp().IsZero() {
		var targetResources *types.ManifestResources

		cacheFilePath := newManifestCache(r.ManifestCacheRoot, spec)
		if err := cacheFilePath.Clean(); err != nil {
			logger.Info("cleaning manifest cache failed")
			r.Event(obj, "Warning", "ManifestCacheCleanup", err.Error())
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return r.ssaStatus(ctx, obj)
		}
		cacheFile := cacheFilePath.ReadYAML()

		if cacheFile.GetRawError() != nil {
			renderStart := time.Now()
			logger.Info(
				"no cached manifest, rendering again",
				"hash", cacheFilePath.hash,
				"path", cacheFilePath.String(),
			)
			release, err := clients.Install().Run(chrt, spec.Values)
			if err != nil {
				logger.Info("rendering failed", "time", time.Since(renderStart))
				r.Event(obj, "Warning", "HelmRenderRun", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			}
			logger.Info("rendering finished", "time", time.Since(renderStart))
			targetResources, err = util.ParseManifestStringToObjects(release.Manifest)
			if err != nil {
				r.Event(obj, "Warning", "ManifestParsing", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			}
			if err := util.WriteToFile(cacheFilePath.String(), []byte(release.Manifest)); err != nil {
				r.Event(obj, "Warning", "ManifestWriting", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			}
		} else {
			logger.V(util.DebugLogLevel).Info(
				"reuse manifest from cache",
				"hash", cacheFilePath.hash,
			)
			targetResources, err = util.ParseManifestStringToObjects(cacheFile.GetContent())
			if err != nil {
				r.Event(obj, "Warning", "ManifestParsing", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			}
		}

		for _, transform := range r.PostRenderTransforms {
			if err := transform(ctx, obj, targetResources); err != nil {
				r.Event(obj, "Warning", "PostRenderTransform", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			}
		}

		target, err = resourceConverter.ConvertResourcesFromManifest(targetResources)
		if err != nil {
			r.Event(obj, "Warning", "TargetResourceParsing", err.Error())
			resourceCondition.Status = metav1.ConditionFalse
			meta.SetStatusCondition(&status.Conditions, resourceCondition)
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return r.ssaStatus(ctx, obj)
		}
	} else {
		target = kube.ResourceList{}
	}

	if current, err = resourceConverter.ConvertStatusToResources(status); err != nil {
		r.Event(obj, "Warning", "CurrentResourceParsing", err.Error())
		resourceCondition.Status = metav1.ConditionFalse
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	if !meta.IsStatusConditionTrue(status.Conditions, resourceCondition.Type) {
		r.Event(obj, "Normal", resourceCondition.Reason, resourceCondition.Message)
		resourceCondition.Status = metav1.ConditionTrue
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
		obj.SetStatus(status.WithOperation(resourceCondition.Message))
		return r.ssaStatus(ctx, obj)
	}

	toDelete := current.Difference(target)
	if deleted, err := ConcurrentCleanup(clients).Run(ctx, toDelete); err != nil {
		r.Event(obj, "Warning", "Deletion", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	} else if !deleted {
		r.Event(obj, "Normal", "Deletion", "deletion not succeeded yet")
		return ctrl.Result{Requeue: true}, nil
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		if r.DeleteCRDsOnUninstall {
			if deleted, err := ConcurrentCleanup(clients).Run(ctx, crds); err != nil {
				r.Event(obj, "Warning", "CRDUninstallation", err.Error())
				obj.SetStatus(status.WithState(StateError).WithErr(err))
				return r.ssaStatus(ctx, obj)
			} else if !deleted {
				waitingMsg := "waiting for crds to be uninstalled"
				r.Event(obj, "Normal", "CRDUninstallation", waitingMsg)
				obj.SetStatus(status.WithOperation(waitingMsg))
				return r.ssaStatus(ctx, obj)
			}
		}

		if controllerutil.RemoveFinalizer(obj, r.Finalizer) {
			return ctrl.Result{}, r.Update(ctx, obj) // here we cannot SSA since the Patching out will not work.
		}

		waitingMsg := "waiting as other finalizers are present"
		r.Event(obj, "Normal", "FinalizerRemoval", waitingMsg)
		obj.SetStatus(status.WithState(StateDeleting).WithOperation(waitingMsg))
		return r.ssaStatus(ctx, obj)
	}

	if err = ConcurrentSSA(clients, r.FieldOwner).Run(ctx, target); err != nil {
		r.Event(obj, "Warning", "ServerSideApply", err.Error())
	}

	if err != nil {
		installationCondition.Status = metav1.ConditionFalse
		meta.SetStatusCondition(&status.Conditions, installationCondition)
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	status = resourceConverter.ConvertSyncedToNewStatus(status, target)

	for i := range r.PostRuns {
		if err := r.PostRuns[i](ctx, r.Client, obj); err != nil {
			r.Event(obj, "Warning", "PostRun", err.Error())
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return r.ssaStatus(ctx, obj)
		}
	}

	resourcesReady, err := readyCheck.Run(ctx, target)
	if err != nil {
		r.Event(obj, "Warning", "ReadyCheck", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return r.ssaStatus(ctx, obj)
	}

	if !resourcesReady {
		waitingMsg := "waiting for resources to become ready"
		r.Event(obj, "Normal", "ResourceReadyCheck", waitingMsg)
		obj.SetStatus(status.WithState(StateProcessing).WithOperation(waitingMsg))
		return r.ssaStatus(ctx, obj)
	}

	if !meta.IsStatusConditionTrue(status.Conditions, installationCondition.Type) || status.State != StateReady {
		r.Event(obj, "Normal", installationCondition.Reason, installationCondition.Message)
		installationCondition.Status = metav1.ConditionTrue
		meta.SetStatusCondition(&status.Conditions, installationCondition)
		obj.SetStatus(status.WithState(StateReady).WithOperation(installationCondition.Message))
		return r.ssaStatus(ctx, obj)
	}

	return r.CtrlOnSuccess, nil
}

func (r *ManifestReconciler) initializeClients(
	ctx context.Context, obj Object, spec *ManifestSpec,
) (*manifestClient.SingletonClients, error) {
	var err error
	var clients *manifestClient.SingletonClients

	clientsCacheKey := cacheKeyFromObject(ctx, obj)

	if clients = r.GetClients(clientsCacheKey); clients == nil {
		clients, err = manifestClient.NewSingletonClients(
			&types.ClusterInfo{
				Config: r.Config,
			}, log.FromContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		clients.Install().Atomic = false
		clients.Install().Replace = true
		clients.Install().DryRun = true
		clients.Install().IncludeCRDs = false
		clients.Install().CreateNamespace = r.CreateNamespace
		clients.Install().UseReleaseName = false
		clients.Install().IsUpgrade = true
		clients.Install().DisableHooks = true
		clients.Install().DisableOpenAPIValidation = true
		if clients.Install().Version == "" && clients.Install().Devel {
			clients.Install().Version = ">0.0.0-0"
		}
		clients.Install().ReleaseName = spec.ManifestName
		r.SetClients(clientsCacheKey, clients)
	}

	clients.Install().Namespace = r.Namespace
	clients.KubeClient().Namespace = r.Namespace

	if r.Namespace != metav1.NamespaceNone && r.Namespace != metav1.NamespaceDefault &&
		clients.Install().CreateNamespace {
		err := clients.Patch(
			ctx, &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
				ObjectMeta: metav1.ObjectMeta{Name: r.Namespace},
			}, client.Apply, client.ForceOwnership, r.FieldOwner,
		)
		if err != nil {
			return nil, err
		}
	}

	return clients, nil
}

func cacheKeyFromObject(ctx context.Context, resource client.Object) client.ObjectKey {
	logger := log.FromContext(ctx)

	if resource == nil {
		return client.ObjectKey{}
	}

	label, err := util.GetResourceLabel(resource, manifestLabels.CacheKey)
	objectKey := client.ObjectKeyFromObject(resource)
	var labelErr *types.LabelNotFoundError
	if errors.As(err, &labelErr) {
		logger.V(util.DebugLogLevel).Info(
			manifestLabels.CacheKey+" missing on resource, it will be cached "+
				"based on resource name and namespace.",
			"resource", objectKey,
		)
		return objectKey
	}

	logger.V(util.DebugLogLevel).Info(
		"resource will be cached based on "+manifestLabels.CacheKey,
		"resource", objectKey,
		"label", manifestLabels.CacheKey,
		"labelValue", label,
	)

	return client.ObjectKey{Name: label, Namespace: resource.GetNamespace()}
}

func (r *ManifestReconciler) ssaStatus(ctx context.Context, obj client.Object) (ctrl.Result, error) {
	obj.SetResourceVersion("")
	return ctrl.Result{Requeue: true}, r.Status().Patch(ctx, obj, client.Apply, client.ForceOwnership, r.FieldOwner)
}

func (r *ManifestReconciler) ssa(ctx context.Context, obj client.Object) (ctrl.Result, error) {
	obj.SetResourceVersion("")
	return ctrl.Result{Requeue: true}, r.Patch(ctx, obj, client.Apply, client.ForceOwnership, r.FieldOwner)
}
