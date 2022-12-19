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
		WithPermanentConsistencyCheck(false),
		WithSingletonClientCache(NewMemorySingletonClientCache()),
		WithManifestCache(os.TempDir()),
	)
}

type Options struct {
	record.EventRecorder
	Config *rest.Config
	client.Client
	TargetClient client.Client

	SpecResolver
	ClientCache
	ManifestCache
	CustomReadyCheck ReadyCheck

	Namespace       string
	CreateNamespace bool

	Finalizer string

	ServerSideApply bool
	FieldOwner      client.FieldOwner

	PostRenderTransforms []ObjectTransform

	PostRuns   []PostRun
	PreDeletes []PreDelete

	DeletePrerequisitesOnUninstall bool

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

func WithSpecResolver(resolver SpecResolver) SpecResolverOption {
	return SpecResolverOption{resolver}
}

type SpecResolverOption struct {
	SpecResolver
}

func (o SpecResolverOption) Apply(options *Options) {
	options.SpecResolver = o
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

// Hook defines a Hook into the declarative reconciliation
// skr is the runtime cluster
// kcp is the control-plane cluster
// obj is guaranteed to be the reconciled object and also to always preside in kcp.
type Hook func(ctx context.Context, skr Client, kcp client.Client, obj Object) error

type (
	PostRun   Hook
	PreDelete Hook
)

type WithPostRun []PostRun

func (o WithPostRun) Apply(options *Options) {
	options.PostRuns = append(options.PostRuns, o...)
}

type WithPreDelete []PreDelete

func (o WithPreDelete) Apply(options *Options) {
	options.PreDeletes = append(options.PreDeletes, o...)
}

type WithPeriodicConsistencyCheck time.Duration

func (o WithPeriodicConsistencyCheck) Apply(options *Options) {
	options.CtrlOnSuccess.RequeueAfter = time.Duration(o)
}

type WithPermanentConsistencyCheck bool

func (o WithPermanentConsistencyCheck) Apply(options *Options) {
	if o {
		options.CtrlOnSuccess = ctrl.Result{Requeue: true}
	} else {
		options.CtrlOnSuccess = ctrl.Result{}
	}
}

type WithSingletonClientCacheOption struct {
	ClientCache
}

func WithSingletonClientCache(cache ClientCache) WithSingletonClientCacheOption {
	return WithSingletonClientCacheOption{ClientCache: cache}
}

func (o WithSingletonClientCacheOption) Apply(options *Options) {
	options.ClientCache = o
}

type WithDeleteCRDsOnUninstall bool

func (o WithDeleteCRDsOnUninstall) Apply(options *Options) {
	options.DeletePrerequisitesOnUninstall = bool(o)
}

type ManifestCache string

const NoManifestCache ManifestCache = "no-cache"

type WithManifestCache ManifestCache

func (o WithManifestCache) Apply(options *Options) {
	options.ManifestCache = ManifestCache(o)
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

func WithRemoteTargetCluster(clnt client.Client) WithRemoteTargetClusterOption {
	return WithRemoteTargetClusterOption{Client: clnt}
}

type WithRemoteTargetClusterOption struct {
	client.Client
}

func (o WithRemoteTargetClusterOption) Apply(options *Options) {
	options.TargetClient = o.Client
}
