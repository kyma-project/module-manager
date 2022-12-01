package v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	manifestClient "github.com/kyma-project/module-manager/pkg/client"
	manifestLabels "github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	FinalizerDefault  = "declarative.kyma-project.io/finalizer"
	FieldOwnerDefault = "declarative.kyma-project.io/applier"
)

func New(mgr manager.Manager, objType Object, options ...Option) *ManifestReconciler {
	r := &ManifestReconciler{}
	r.prototype = objType
	r.Client = mgr.GetClient()
	r.SingletonClientCache = NewMemorySingletonClientCache()
	r.Config = mgr.GetConfig()
	r.EventRecorder = mgr.GetEventRecorderFor(objType.GetName())
	for i := range options {
		options[i].Apply(r)
	}

	if r.Namespace == "" {
		r.Namespace = v1.NamespaceDefault
	}
	if r.Values == nil {
		r.Values = WithValues{}
	}

	if r.FieldOwner == "" {
		r.FieldOwner = FieldOwnerDefault
	}
	if r.Finalizer == "" {
		r.Finalizer = FinalizerDefault
	}
	if r.CustomResourceLabels == nil {
		r.CustomResourceLabels = labels.Set{}
	}

	return r
}

type ManifestReconciler struct {
	prototype runtime.Object
	client.Client
	Config *rest.Config
	record.EventRecorder
	ManifestSpecSource
	SingletonClientCache
	Namespace            string
	Values               map[string]interface{}
	FieldOwner           client.FieldOwner
	Finalizer            string
	CustomResourceLabels labels.Set
	PostRenderTransforms []types.ObjectTransform
}

func (r *ManifestReconciler) GetPrototype() (Object, error) {
	obj, ok := r.prototype.DeepCopyObject().(Object)
	if !ok {
		return nil, fmt.Errorf("invalid custom resource object type for reconciliation")
	}
	return obj, nil
}

type ManifestSpecSource interface {
	ResolveManifestSpec(ctx context.Context, object Object) (*ManifestSpec, error)
}

type Scope struct {
	*ManifestSpec
	*types.ClusterInfo
	Object
}
type ManifestSpec struct {
	ManifestName string
	ChartPath    string
	Values       map[string]interface{}
}

func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	obj, err := r.GetPrototype()
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		logger.Info(req.NamespacedName.String() + " got deleted!")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Required for SSA
	obj.SetManagedFields(nil)

	status := obj.GetStatus()

	if !obj.GetDeletionTimestamp().IsZero() && obj.GetStatus().State != StateDeleting {
		obj.SetStatus(status.WithState(StateDeleting))
		return r.ssaStatus(ctx, obj)
	}

	// add finalizer
	if controllerutil.AddFinalizer(obj, r.Finalizer) {
		return r.ssa(ctx, obj)
	}

	crdCondition := metav1.Condition{
		Type:    "CRDs",
		Reason:  "Synced",
		Status:  metav1.ConditionFalse,
		Message: "CRDs are installed and ready for use",
	}
	resourceCondition := metav1.Condition{
		Type:    "Resources",
		Reason:  "SyncResourceParsed",
		Status:  metav1.ConditionFalse,
		Message: "Resources are parsed and ready for use",
	}

	// Processing
	if status.State == "" {
		status.Conditions = make([]metav1.Condition, 0, 2)
		meta.SetStatusCondition(&status.Conditions, crdCondition)
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
		status.Synced = make([]Resource, 0)
		obj.SetStatus(status.WithState(StateProcessing))
		return r.ssaStatus(ctx, obj)
	}

	spec, err := r.ResolveManifestSpec(ctx, obj)
	if err != nil {
		r.Event(obj, "Warning", "ResolveManifestSpec", err.Error())
		obj.SetStatus(status.WithState(StateError))
		return r.ssaStatus(ctx, obj)
	}
	for key := range r.Values {
		spec.Values[key] = r.Values[key]
	}

	scope := &Scope{
		ManifestSpec: spec,
		ClusterInfo: &types.ClusterInfo{
			Client: r.Client,
			Config: r.Config,
		},
		Object: obj,
	}

	clientsCacheKey := discoverCacheKey(ctx, scope.Object)
	var clients *manifestClient.SingletonClients

	if clients = r.GetClients(clientsCacheKey); clients == nil {
		clients, err = manifestClient.NewSingletonClients(scope.ClusterInfo, logger)
		if err != nil {
			r.Event(obj, "Warning", "NewSingletonClients", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}

		clients.Install().Atomic = false
		clients.Install().Replace = true
		clients.Install().DryRun = true
		clients.Install().IncludeCRDs = false
		clients.Install().CreateNamespace = true
		clients.Install().UseReleaseName = false
		clients.Install().IsUpgrade = true
		if clients.Install().Version == "" && clients.Install().Devel {
			clients.Install().Version = ">0.0.0-0"
		}

		clients.Install().ReleaseName = scope.ManifestName
		r.SetClients(clientsCacheKey, clients)
	}

	if clients.Install().CreateNamespace && r.Namespace != "" || r.Namespace != "default" {
		clients.Install().Namespace = r.Namespace
		clients.KubeClient().Namespace = r.Namespace

		err := clients.Patch(ctx, &v1.Namespace{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
			ObjectMeta: metav1.ObjectMeta{Name: r.Namespace},
		}, client.Apply, client.ForceOwnership, r.FieldOwner)

		if err != nil {
			r.Event(obj, "Warning", "NamespaceSSA", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	}

	chrt, err := loader.Load(scope.ChartPath)
	if err != nil {
		r.Event(obj, "Warning", "ChartLoading", err.Error())
		obj.SetStatus(status.WithState(StateError))
		return r.ssaStatus(ctx, obj)
	}

	if obj.GetDeletionTimestamp().IsZero() && !meta.IsStatusConditionTrue(status.Conditions, "CRDs") {
		crdFiles := chrt.CRDObjects()
		var crdManifest bytes.Buffer
		for i := range crdFiles {
			crdManifest.Write(append(bytes.TrimPrefix(crdFiles[i].File.Data, []byte("---\n")), '\n'))
		}
		crdsObjects, err := util.ParseManifestStringToObjects(crdManifest.String())
		if err != nil {
			r.Event(obj, "Warning", "ManifestParsing", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
		var crds kube.ResourceList
		errs := make([]error, 0, len(crdsObjects.Items))
		for _, crd := range crdsObjects.Items {
			crdInfo, err := clients.ResourceInfo(crd, true)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			crds = append(crds, crdInfo)
		}

		if len(errs) > 0 {
			r.Event(obj, "Warning", "CRDParsing", types.NewMultiError(errs).Error())
			crdCondition.Status = metav1.ConditionFalse
			meta.SetStatusCondition(&status.Conditions, crdCondition)
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}

		crdInstallWaitGroup := sync.WaitGroup{}
		errChan := make(chan error, len(crds))
		createCRD := func(i int) {
			defer crdInstallWaitGroup.Done()
			_, err := clients.KubeClient().Create(kube.ResourceList{crds[i]})
			errChan <- err
		}

		for i := range crds {
			crdInstallWaitGroup.Add(1)
			go createCRD(i)
		}
		crdInstallWaitGroup.Wait()
		close(errChan)

		for err := range errChan {
			if err == nil || apierrors.IsAlreadyExists(err) {
				continue
			}
			r.Event(obj, "Warning", "CRDInstallation", err.Error())
			meta.SetStatusCondition(&status.Conditions, crdCondition)
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}

		restMapper, _ := clients.ToRESTMapper()
		meta.MaybeResetRESTMapper(restMapper)
		crdCondition.Status = metav1.ConditionTrue
		meta.SetStatusCondition(&status.Conditions, crdCondition)
		return r.ssaStatus(ctx, obj)
	}

	var targetResources *types.ManifestResources

	manifestFilePath := filepath.Join(scope.ChartPath, "manifest")
	cacheFilePath := filepath.Join(manifestFilePath, scope.ManifestName)
	hashedValues, _ := util.CalculateHash(spec.Values)
	cacheFilePath = fmt.Sprintf("%s-%v.yaml", cacheFilePath, hashedValues)
	err = filepath.Walk(manifestFilePath, func(path string, info fs.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		oldCachedManifest := filepath.Join(manifestFilePath, info.Name())
		if oldCachedManifest != cacheFilePath {
			return os.Remove(oldCachedManifest)
		}
		return nil
	})
	cacheFile := types.NewParsedFile(util.GetStringifiedYamlFromFilePath(cacheFilePath))

	if cacheFile.GetRawError() != nil {
		release, err := clients.Install().Run(chrt, spec.Values)
		if err != nil {
			r.Event(obj, "Warning", "HelmRenderRun", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
		targetResources, err = util.ParseManifestStringToObjects(release.Manifest)
		if err != nil {
			r.Event(obj, "Warning", "ManifestParsing", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
		if err := util.WriteToFile(cacheFilePath, []byte(release.Manifest)); err != nil {
			r.Event(obj, "Warning", "ManifestWriting", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	} else {
		targetResources, err = util.ParseManifestStringToObjects(cacheFile.GetContent())
		if err != nil {
			r.Event(obj, "Warning", "ManifestParsing", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	}

	for _, transform := range r.PostRenderTransforms {
		if err := transform(ctx, obj, targetResources); err != nil {
			r.Event(obj, "Warning", "ManifestParsing", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	}

	// CUSTOMIZATION
	for _, targetResource := range targetResources.Items {
		lbls := targetResource.GetLabels()
		if lbls == nil {
			lbls = labels.Set{}
		}
		for s := range r.CustomResourceLabels {
			lbls[s] = r.CustomResourceLabels[s]
		}
		targetResource.SetLabels(lbls)
	}

	var target kube.ResourceList
	if obj.GetDeletionTimestamp().IsZero() {
		errs := make([]error, 0, len(targetResources.Items))
		for _, obj := range targetResources.Items {
			resourceInfo, err := clients.ResourceInfo(obj, true)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			target = append(target, resourceInfo)
		}
		if len(errs) > 0 {
			r.Event(obj, "Warning", "TargetResourceParsing", types.NewMultiError(errs).Error())
			resourceCondition.Status = metav1.ConditionFalse
			meta.SetStatusCondition(&status.Conditions, resourceCondition)
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		} else {
			resourceCondition.Status = metav1.ConditionTrue
			meta.SetStatusCondition(&status.Conditions, resourceCondition)
		}
	} else {
		target = kube.ResourceList{}
	}

	var current kube.ResourceList
	errs := make([]error, 0, len(status.Synced))
	for _, synced := range status.Synced {
		unstruct := &unstructured.Unstructured{}
		unstruct.SetName(synced.Name)
		unstruct.SetNamespace(synced.Namespace)
		unstruct.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   synced.Group,
			Version: synced.Version,
			Kind:    synced.Kind,
		})
		resourceInfo, err := clients.ResourceInfo(unstruct, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		current = append(current, resourceInfo)
	}
	if len(errs) > 0 {
		r.Event(obj, "Warning", "CurrentResourceParsing", types.NewMultiError(errs).Error())
		resourceCondition.Status = "NotReady"
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
		obj.SetStatus(status.WithState(StateError))
		return r.ssaStatus(ctx, obj)
	} else {
		resourceCondition.Status = metav1.ConditionTrue
		meta.SetStatusCondition(&status.Conditions, resourceCondition)
	}

	toDelete := current.Difference(target)
	if len(toDelete) > 0 {
		_, errs = clients.KubeClient().Delete(toDelete)
		if errs != nil {
			r.Event(obj, "Warning", "Deletion", types.NewMultiError(errs).Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	}

	for i := range toDelete {
		err := toDelete[i].Get()
		if err == nil || !apierrors.IsNotFound(err) {
			r.Event(obj, "Warning", "DeletionCheck", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		if controllerutil.RemoveFinalizer(obj, r.Finalizer) {
			return r.ssa(ctx, obj)
		}

		obj.SetStatus(status.WithState(StateDeleting))
		return r.ssaStatus(ctx, obj)
	}

	// this catches all resources in target that were created externally or if the synced
	// info is not up-to-date. If there is one missing in current its added.
	for i := range target {
		if target[i].Get() == nil && !current.Contains(target[i]) {
			current.Append(target[i])
		}
	}

	_, err = clients.KubeClient().Update(current, target, false)
	if err != nil {
		r.Event(obj, "Warning", "Update", err.Error())
		obj.SetStatus(status.WithState(StateError))
		return r.ssaStatus(ctx, obj)
	}

	status.Synced = make([]Resource, 0, len(target))
	for _, info := range target {
		status.Synced = append(status.Synced, Resource{
			Name:      info.Name,
			Namespace: info.Namespace,
			GroupVersionKind: metav1.GroupVersionKind{
				Group:   info.Mapping.GroupVersionKind.Group,
				Version: info.Mapping.GroupVersionKind.Version,
				Kind:    info.Mapping.GroupVersionKind.Kind,
			},
		})
	}

	resourcesReady := true
	clientSet, _ := clients.KubernetesClientSet()
	checker := kube.NewReadyChecker(clientSet, func(format string, args ...interface{}) {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf(format, args...))
	}, kube.PausedAsReady(true), kube.CheckJobs(true))

	for i := range target {
		ready, err := checker.IsReady(ctx, target[i])
		if err != nil {
			r.Event(obj, "Warning", "ReadyCheck", err.Error())
			obj.SetStatus(status.WithState(StateError))
			return r.ssaStatus(ctx, obj)
		}
		if !ready {
			resourcesReady = false
			break
		}
	}

	if !resourcesReady {
		r.Event(obj, "Normal", "ResourceReadyCheck", "resources are not yet ready.")
		obj.SetStatus(status.WithState(StateProcessing))
		return r.ssaStatus(ctx, obj)
	}

	r.Event(obj, "Normal", "ResourceReadyCheck", "resources are ready!")
	obj.SetStatus(status.WithState(StateReady))
	return ctrl.Result{}, r.Patch(ctx, obj, client.Apply, client.ForceOwnership, r.FieldOwner)
}

func (r *ManifestReconciler) ssaStatus(ctx context.Context, obj client.Object) (ctrl.Result, error) {
	return ctrl.Result{Requeue: true}, r.Status().Patch(ctx, obj, client.Apply, client.ForceOwnership, r.FieldOwner)
}

func (r *ManifestReconciler) ssa(ctx context.Context, obj client.Object) (ctrl.Result, error) {
	return ctrl.Result{Requeue: true}, r.Patch(ctx, obj, client.Apply, client.ForceOwnership, r.FieldOwner)
}

func discoverCacheKey(ctx context.Context, resource client.Object) client.ObjectKey {
	logger := log.FromContext(ctx)

	if resource == nil {
		return client.ObjectKey{}
	}

	label, err := util.GetResourceLabel(resource, manifestLabels.CacheKey)
	objectKey := client.ObjectKeyFromObject(resource)
	var labelErr *types.LabelNotFoundError
	if errors.As(err, &labelErr) {
		logger.V(util.DebugLogLevel).Info(manifestLabels.CacheKey+" missing on resource, it will be cached "+
			"based on resource name and namespace.",
			"resource", objectKey)
		return objectKey
	}

	logger.V(util.DebugLogLevel).Info("resource will be cached based on "+manifestLabels.CacheKey,
		"resource", objectKey,
		"label", manifestLabels.CacheKey,
		"labelValue", label)

	return client.ObjectKey{Name: label, Namespace: resource.GetNamespace()}
}
