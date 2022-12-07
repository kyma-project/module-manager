package v2

import (
	"context"
	"os"
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

func DefaultOptions() *Options {
	return (&Options{}).Apply(
		WithDeleteCRDsOnUninstall(false),
		WithNamespace(metav1.NamespaceDefault, false),
		WithFinalizer(FinalizerDefault),
		WithFieldOwner(FieldOwnerDefault),
		WithPostRenderTransform(
			managedByDeclarativeV2,
			kymaComponentTransform,
			disclaimerTransform,
		),
		WithConsistencyCheckOnCacheReset(true),
		WithSingletonClientCache(NewMemorySingletonClientCache()),
		WithManifestCacheRoot(os.TempDir()),
	)
}

type Options struct {
	record.EventRecorder
	Config *rest.Config
	client.Client

	ManifestSpecSource
	SingletonClientCache
	ManifestCacheRoot string
	CustomReadyCheck  ReadyCheck

	Namespace       string
	CreateNamespace bool

	Finalizer string

	ServerSideApply bool
	FieldOwner      client.FieldOwner

	PostRenderTransforms []ObjectTransform
	PostRuns             []PostRun

	DeleteCRDsOnUninstall bool

	CtrlOnSuccess ctrl.Result
}

type Option interface {
	Apply(options *Options)
}

func (o *Options) Apply(options ...Option) *Options {
	for i := range options {
		options[i].Apply(o)
	}
	return o
}

type WithNamespaceOption struct {
	name            string
	createIfMissing bool
}

func WithNamespace(name string, createIfMissing bool) WithNamespaceOption {
	return WithNamespaceOption{
		name:            name,
		createIfMissing: createIfMissing,
	}
}

func (o WithNamespaceOption) Apply(options *Options) {
	options.Namespace = o.name
	options.CreateNamespace = o.createIfMissing
}

type WithFieldOwner client.FieldOwner

func (o WithFieldOwner) Apply(options *Options) {
	options.FieldOwner = client.FieldOwner(o)
}

type WithFinalizer string

func (o WithFinalizer) Apply(options *Options) {
	options.Finalizer = string(o)
}

type WithManagerOption struct {
	manager.Manager
}

func WithManager(mgr manager.Manager) WithManagerOption {
	return WithManagerOption{Manager: mgr}
}

func (o WithManagerOption) Apply(options *Options) {
	options.EventRecorder = o.GetEventRecorderFor(EventRecorderDefault)
	options.Config = o.GetConfig()
	options.Client = o.GetClient()
}

type WithCustomResourceLabels labels.Set

func (o WithCustomResourceLabels) Apply(options *Options) {
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

func (o ManifestSpecSourceOption) Apply(options *Options) {
	options.ManifestSpecSource = o
}

// StaticManifestSpecSource is a simple static resolver that always uses the same chart and values.
type StaticManifestSpecSource struct {
	ManifestNameFn func(ctx context.Context, obj Object) string
	ChartPath      string
	Values         map[string]any
}

func (m *StaticManifestSpecSource) ResolveManifestSpec(
	ctx context.Context, obj Object,
) (*ManifestSpec, error) {
	return &ManifestSpec{
		ManifestName: m.ManifestNameFn(ctx, obj),
		ChartPath:    m.ChartPath,
		Values:       m.Values,
	}, nil
}

type ObjectTransform = func(context.Context, Object, *types.ManifestResources) error

func WithPostRenderTransform(transforms ...ObjectTransform) PostRenderTransformOption {
	return PostRenderTransformOption{transforms}
}

type PostRenderTransformOption struct {
	ObjectTransforms []ObjectTransform
}

func (o PostRenderTransformOption) Apply(options *Options) {
	options.PostRenderTransforms = append(options.PostRenderTransforms, o.ObjectTransforms...)
}

type PostRun = func(
	ctx context.Context,
	client client.Client,
	obj Object,
) error

type WithPostRun []PostRun

func (o WithPostRun) Apply(options *Options) {
	options.PostRuns = append(options.PostRuns, o...)
}

type WithPeriodicConsistencyCheck time.Duration

func (o WithPeriodicConsistencyCheck) Apply(options *Options) {
	options.CtrlOnSuccess.RequeueAfter = time.Duration(o)
}

type WithConsistencyCheckOnCacheReset bool

func (o WithConsistencyCheckOnCacheReset) Apply(options *Options) {
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

func (o WithSingletonClientCacheOption) Apply(options *Options) {
	options.SingletonClientCache = o
}

type WithDeleteCRDsOnUninstall bool

func (o WithDeleteCRDsOnUninstall) Apply(options *Options) {
	options.DeleteCRDsOnUninstall = bool(o)
}

type WithManifestCacheRoot string

func (o WithManifestCacheRoot) Apply(options *Options) {
	options.ManifestCacheRoot = string(o)
}

type WithCustomReadyCheckOption struct {
	ReadyCheck
}

func WithCustomReadyCheck(check ReadyCheck) WithCustomReadyCheckOption {
	return WithCustomReadyCheckOption{ReadyCheck: check}
}

func (o WithCustomReadyCheckOption) Apply(options *Options) {
	options.CustomReadyCheck = o
}
