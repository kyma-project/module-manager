package v2

import (
	"context"

	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReconcilerOptions struct {
	ManifestSpecSource

	Namespace string

	Finalizer string

	ServerSideApply bool
	FieldOwner      client.FieldOwner

	PostRenderTransforms []ObjectTransform
	PostRuns             []PostRun
}

type Option interface {
	Apply(options *ReconcilerOptions)
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

type WithServerSideApply bool

func (o WithServerSideApply) Apply(options *ReconcilerOptions) {
	options.ServerSideApply = bool(o)
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
