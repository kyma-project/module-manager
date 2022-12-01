package v2

import (
	"context"

	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReconcilerOptions struct {
	ManifestSpecSource
	Namespace            string
	Values               map[string]interface{}
	FieldOwner           client.FieldOwner
	Finalizer            string
	CustomResourceLabels labels.Set
	PostRenderTransforms []types.ObjectTransform
	ServerSideApply      bool
}

type Option interface {
	Apply(options *ReconcilerOptions)
}

type WithNamespace string

func (o WithNamespace) Apply(options *ReconcilerOptions) {
	options.Namespace = string(o)
}

type WithValues types.Flags

func (o WithValues) Apply(options *ReconcilerOptions) {
	options.Values = o
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
	options.CustomResourceLabels = labels.Set(o)
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

func WithPostRenderTransform(transforms ...types.ObjectTransform) PostRenderTransformOption {
	return PostRenderTransformOption{transforms}
}

type PostRenderTransformOption struct {
	ObjectTransforms []types.ObjectTransform
}

func (o PostRenderTransformOption) Apply(options *ReconcilerOptions) {
	options.PostRenderTransforms = o.ObjectTransforms
}

type WithServerSideApply bool

func (o WithServerSideApply) Apply(options *ReconcilerOptions) {
	options.ServerSideApply = bool(o)
}
