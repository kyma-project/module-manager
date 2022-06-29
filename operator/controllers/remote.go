package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"path"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type RemoteInterface struct {
	NativeClient client.Client
	CustomClient client.Client
	NativeObject *v1alpha1.Manifest
}

func NewRemoteInterface(ctx context.Context, nativeClient client.Client, nativeObject *v1alpha1.Manifest) (*RemoteInterface, *v1alpha1.Manifest, error) {
	namespacedName := client.ObjectKeyFromObject(nativeObject)
	kymaOwner, ok := nativeObject.Labels[labels.ComponentOwner]
	if !ok {
		return nil, nil, fmt.Errorf(
			fmt.Sprintf("label %s not found on manifest resource %s", labels.ComponentOwner, namespacedName))
	}

	remoteCluster := &custom.ClusterClient{DefaultClient: nativeClient}
	restConfig, err := remoteCluster.GetRestConfig(ctx, kymaOwner, nativeObject.Namespace)
	if err != nil {
		return nil, nil, err
	}

	customClient, err := remoteCluster.GetNewClient(restConfig, client.Options{
		Scheme: nativeClient.Scheme(),
	})
	if err != nil {
		return nil, nil, err
	}

	remoteInterface := &RemoteInterface{
		NativeObject: nativeObject,
		NativeClient: nativeClient,
		CustomClient: customClient,
	}

	if meta.IsNoMatchError(err) {
		if err := remoteInterface.CreateCRD(ctx); err != nil {
			return nil, nil, err
		}
	}

	// check for remote resource
	remoteManifest, err := remoteInterface.GetLatestRemote(ctx)

	if client.IgnoreNotFound(err) != nil {
		// unknown error
		return nil, nil, err
	} else if err != nil {
		// remote resource doesn't exist
		remoteManifest, err = remoteInterface.createRemote(ctx)
		if err != nil {
			return nil, nil, err
		}
	}

	// check finalizer
	if !controllerutil.ContainsFinalizer(remoteManifest, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(remoteManifest, labels.ManifestFinalizer)
		if err = remoteInterface.Update(ctx, remoteManifest); err != nil {
			return nil, nil, err
		}
	}

	return remoteInterface, remoteManifest, nil
}

func (r *RemoteInterface) createRemote(ctx context.Context) (*v1alpha1.Manifest, error) {
	remoteManifest := &v1alpha1.Manifest{}
	remoteManifest.Name = r.NativeObject.Name
	remoteManifest.Namespace = r.NativeObject.Namespace
	remoteManifest.Spec = r.NativeObject.Spec
	controllerutil.AddFinalizer(remoteManifest, labels.ManifestFinalizer)
	err := r.CustomClient.Create(ctx, remoteManifest)
	return remoteManifest, err
}

func (r *RemoteInterface) SpecSyncToNative(ctx context.Context, remoteManifest *v1alpha1.Manifest) (bool, error) {
	if remoteManifest.Status.ObservedGeneration == 0 {
		// observed generation missing - remote resource freshly installed!
		return true, nil
	}

	if remoteManifest.Status.ObservedGeneration != remoteManifest.Generation {
		// outdated
		r.NativeObject.Spec = remoteManifest.Spec
		remoteManifest.Status.ObservedGeneration = remoteManifest.Generation
		return false, r.UpdateStatus(ctx, remoteManifest)
	}

	return true, nil
}

func (r *RemoteInterface) StateSyncToRemote(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	if r.NativeObject.Status.State != remoteManifest.Status.State {
		remoteManifest.Status.State = r.NativeObject.Status.State
		remoteManifest.Status.ObservedGeneration = remoteManifest.Generation
		return r.UpdateStatus(ctx, remoteManifest)
	}
	return nil
}

func (r *RemoteInterface) SpecSyncToRemote(ctx context.Context, remoteManifest *v1alpha1.Manifest) (*v1alpha1.Manifest, error) {
	// ignore cases where native object has never been reconciled!
	if r.NativeObject.Status.ObservedGeneration == 0 {
		return remoteManifest, nil
	}

	if r.NativeObject.Status.ObservedGeneration != r.NativeObject.Generation {
		// remove finalizer
		if err := r.RemoveFinalizerOnRemote(ctx, remoteManifest); err != nil {
			return remoteManifest, err
		}

		// trigger delete on remote
		if err := r.HandleDeletingState(ctx, remoteManifest); err != nil {
			return remoteManifest, err
		}

		// create fresh object
		return r.createRemote(ctx)
	}

	return remoteManifest, nil
}

func (r *RemoteInterface) CreateCRD(ctx context.Context) error {
	crd := v1.CustomResourceDefinition{}
	err := r.CustomClient.Get(ctx, client.ObjectKey{
		// TODO: Change "manifests" with updated api value
		Name: fmt.Sprintf("%s.%s", "manifests", v1alpha1.GroupVersion.Group),
	}, &crd)
	if err == nil {
		return errors.NewAlreadyExists(schema.GroupResource{Group: v1alpha1.GroupVersion.Group}, crd.GetName())
	}

	// TODO: for demo purposes, should ideally be close to the control plane
	crds, err := manifest.GetCRDsFromPath(ctx, path.Join("./../", "api", "config", "crd", "bases"))
	if err != nil {
		return err
	}

	for _, crd := range crds {
		if err = r.CustomClient.Create(ctx, crd); err != nil {
			return err
		}
	}
	return nil
}

func (r *RemoteInterface) HandleDeletingState(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	return r.CustomClient.Delete(ctx, remoteManifest)
}

func (r *RemoteInterface) RemoveFinalizerOnRemote(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	controllerutil.RemoveFinalizer(remoteManifest, labels.ManifestFinalizer)
	return r.Update(ctx, remoteManifest)
}

func (r *RemoteInterface) Update(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	return r.CustomClient.Update(ctx, remoteManifest)
}

func (r *RemoteInterface) UpdateStatus(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	return r.CustomClient.Status().Update(ctx, remoteManifest)
}

func (r *RemoteInterface) GetLatestRemote(ctx context.Context) (*v1alpha1.Manifest, error) {
	remoteObject := &v1alpha1.Manifest{}
	err := r.CustomClient.Get(ctx, client.ObjectKeyFromObject(r.NativeObject), remoteObject)
	return remoteObject, err
}
