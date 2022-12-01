package v2

import (
	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Option interface {
	Apply(reconciler *ManifestReconciler)
}

type WithNamespace string

func (o WithNamespace) Apply(reconciler *ManifestReconciler) {
	reconciler.Namespace = string(o)
}

type WithValues types.Flags

func (o WithValues) Apply(reconciler *ManifestReconciler) {
	reconciler.Values = o
}

type WithFieldOwner client.FieldOwner

func (o WithFieldOwner) Apply(reconciler *ManifestReconciler) {
	reconciler.FieldOwner = client.FieldOwner(o)
}

type WithFinalizer string

func (o WithFinalizer) Apply(reconciler *ManifestReconciler) {
	reconciler.Finalizer = string(o)
}

type WithCustomResourceLabels labels.Set

func (o WithCustomResourceLabels) Apply(reconciler *ManifestReconciler) {
	reconciler.CustomResourceLabels = labels.Set(o)
}

func WithManifestSpecSource(source ManifestSpecSource) ManifestSpecSourceOption {
	return ManifestSpecSourceOption{source}
}

type ManifestSpecSourceOption struct {
	ManifestSpecSource
}

func (o ManifestSpecSourceOption) Apply(reconciler *ManifestReconciler) {
	reconciler.ManifestSpecSource = o
}

func WithPostRenderTransform(transforms ...types.ObjectTransform) PostRenderTransformOption {
	return PostRenderTransformOption{transforms}
}

type PostRenderTransformOption struct {
	ObjectTransforms []types.ObjectTransform
}

func (o PostRenderTransformOption) Apply(reconciler *ManifestReconciler) {
	reconciler.PostRenderTransforms = o.ObjectTransforms
}
