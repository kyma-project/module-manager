package v2

import (
	"context"
	"time"

	"github.com/kyma-project/module-manager/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	FinalizerDefault     = "declarative.kyma-project.io/finalizer"
	FieldOwnerDefault    = "declarative.kyma-project.io/applier"
	EventRecorderDefault = "declarative.kyma-project.io/events"
)

func DefaultReconcilerOptions() *ReconcilerOptions {
	return (&ReconcilerOptions{}).Apply(
		WithDeleteCRDsOnUninstall(false),
		WithNamespace(metav1.NamespaceDefault),
		WithFinalizer(FinalizerDefault),
		WithFieldOwner(FieldOwnerDefault),
		WithPostRenderTransform(
			managedByDeclarativeV2,
			kymaComponentTransform,
			disclaimerTransform,
		),
		WithConsistencyCheckOnCacheReset(true),
		WithSingletonClientCache(NewMemorySingletonClientCache()),
	)
}

type ReconcilerOptions struct {
	record.EventRecorder
	Config *rest.Config
	client.Client

	ManifestSpecSource
	SingletonClientCache

	Namespace string
	Finalizer string

	ServerSideApply bool
	FieldOwner      client.FieldOwner

	PostRenderTransforms []ObjectTransform
	PostRuns             []PostRun

	DeleteCRDsOnUninstall bool

	CtrlOnSuccess ctrl.Result
}

type Option interface {
	Apply(options *ReconcilerOptions)
}

func (o *ReconcilerOptions) Apply(options ...Option) *ReconcilerOptions {
	for i := range options {
		options[i].Apply(o)
	}
	return o
}

type WithNamespace string

func (o WithNamespace) Apply(options *ReconcilerOptions) {
	options.Namespace = string(o)
}

type WithFieldOwner client.FieldOwner

func (o WithFieldOwner) Apply(options *ReconcilerOptions) {
	options.FieldOwner = client.FieldOwner(o)
}

type WithFinalizer string

func (o WithFinalizer) Apply(options *ReconcilerOptions) {
	options.Finalizer = string(o)
}

type WithManagerOption struct {
	manager.Manager
}

func WithManager(mgr manager.Manager) WithManagerOption {
	return WithManagerOption{Manager: mgr}
}

func (o WithManagerOption) Apply(options *ReconcilerOptions) {
	options.EventRecorder = o.GetEventRecorderFor(EventRecorderDefault)
	options.Config = o.GetConfig()
	options.Client = o.GetClient()
}

type WithCustomResourceLabels labels.Set

func (o WithCustomResourceLabels) Apply(options *ReconcilerOptions) {
	labelTransform := func(ctx context.Context, object Object, resources *types.ManifestResources) error {
		for _, targetResource := range resources.Items {
			lbls := targetResource.GetLabels()
			if lbls == nil {
				lbls = labels.Set{}
			}
			for s := range o {
				lbls[s] = o[s]
			}
			targetResource.SetLabels(lbls)
		}
		return nil
	}
	options.PostRenderTransforms = append(options.PostRenderTransforms, labelTransform)
}

type ManifestSpecSource interface {
	ResolveManifestSpec(ctx context.Context, object Object) (*ManifestSpec, error)
}

type ManifestSpec struct {
	ManifestName string
	ChartPath    string
	Values       map[string]interface{}
}

func WithManifestSpecSource(source ManifestSpecSource) ManifestSpecSourceOption {
	return ManifestSpecSourceOption{source}
}

type ManifestSpecSourceOption struct {
	ManifestSpecSource
}

func (o ManifestSpecSourceOption) Apply(options *ReconcilerOptions) {
	options.ManifestSpecSource = o
}

type ObjectTransform = func(context.Context, Object, *types.ManifestResources) error

func WithPostRenderTransform(transforms ...ObjectTransform) PostRenderTransformOption {
	return PostRenderTransformOption{transforms}
}

type PostRenderTransformOption struct {
	ObjectTransforms []ObjectTransform
}

func (o PostRenderTransformOption) Apply(options *ReconcilerOptions) {
	options.PostRenderTransforms = append(options.PostRenderTransforms, o.ObjectTransforms...)
}

type PostRun = func(
	ctx context.Context,
	client client.Client,
	obj Object,
) error

type WithPostRun []PostRun

func (o WithPostRun) Apply(options *ReconcilerOptions) {
	options.PostRuns = append(options.PostRuns, o...)
}

type WithPeriodicConsistencyCheck time.Duration

func (o WithPeriodicConsistencyCheck) Apply(options *ReconcilerOptions) {
	options.CtrlOnSuccess.RequeueAfter = time.Duration(o)
}

type WithConsistencyCheckOnCacheReset bool

func (o WithConsistencyCheckOnCacheReset) Apply(options *ReconcilerOptions) {
	if o {
		options.CtrlOnSuccess = ctrl.Result{}
	} else {
		options.CtrlOnSuccess = ctrl.Result{Requeue: true}
	}
}

type WithSingletonClientCacheOption struct {
	SingletonClientCache
}

func WithSingletonClientCache(cache SingletonClientCache) WithSingletonClientCacheOption {
	return WithSingletonClientCacheOption{SingletonClientCache: cache}
}

func (o WithSingletonClientCacheOption) Apply(options *ReconcilerOptions) {
	options.SingletonClientCache = o
}

type WithDeleteCRDsOnUninstall bool

func (o WithDeleteCRDsOnUninstall) Apply(options *ReconcilerOptions) {
	options.DeleteCRDsOnUninstall = bool(o)
}
