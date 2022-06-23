package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
)

type RemoteInterface struct {
	NativeClient client.Client
	CustomClient client.Client
	NativeObject *v1alpha1.Manifest
	RemoteObject *v1alpha1.Manifest
}

const remoteResourceNotSet = "remote resource not set for manifest %s"

func NewRemoteInterface(ctx context.Context, nativeClient client.Client, nativeObject *v1alpha1.Manifest) (*RemoteInterface, error) {
	namespacedName := client.ObjectKeyFromObject(nativeObject)
	kymaOwner, ok := nativeObject.Labels[labels.ComponentOwner]
	if !ok {
		return nil, fmt.Errorf(
			fmt.Sprintf("label %s not found on manifest resource %s", labels.ComponentOwner, namespacedName))
	}

	remoteCluster := &custom.ClusterClient{DefaultClient: nativeClient}
	restConfig, err := remoteCluster.GetRestConfig(ctx, kymaOwner, nativeObject.Namespace)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	customClient, err := remoteCluster.GetNewClient(restConfig, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	remoteObject := &v1alpha1.Manifest{}
	err = customClient.Get(ctx, namespacedName, remoteObject)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}

	remoteInterface := &RemoteInterface{
		NativeObject: nativeObject,
		NativeClient: nativeClient,
		CustomClient: customClient,
	}

	// remote object doesn't exist
	if err != nil {
		remoteObject, err = remoteInterface.createRemote(ctx)
		if err != nil {
			return nil, err
		}
	}

	// check finalizer
	if !controllerutil.ContainsFinalizer(remoteObject, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(remoteObject, labels.ManifestFinalizer)
		if err = customClient.Update(ctx, remoteObject); err != nil {
			return nil, err
		}
	}

	remoteInterface.RemoteObject = remoteObject

	return remoteInterface, nil
}

func (r *RemoteInterface) createRemote(ctx context.Context) (*v1alpha1.Manifest, error) {
	remoteObject := &v1alpha1.Manifest{}
	remoteObject.Name = r.NativeObject.Name
	remoteObject.Namespace = r.NativeObject.Namespace
	remoteObject.Spec = *r.NativeObject.Spec.DeepCopy()
	controllerutil.AddFinalizer(remoteObject, labels.ManifestFinalizer)
	if err := r.CustomClient.Create(ctx, remoteObject); err != nil {
		return nil, err
	}
	return remoteObject, nil
}

func (r *RemoteInterface) IsNativeSpecSynced() bool {
	if r.RemoteObject == nil {
		return false
	}
	expectedGeneration := strconv.FormatInt(r.RemoteObject.Generation, 10)
	remoteGenerationLastVisited, ok := r.NativeObject.Labels[labels.RemoteGeneration]
	if !ok {
		// label missing
		r.NativeObject.Labels[labels.RemoteGeneration] = expectedGeneration
		return false
	}

	if remoteGenerationLastVisited != expectedGeneration {
		// outdated
		r.NativeObject.Spec = *r.RemoteObject.Spec.DeepCopy()
		r.NativeObject.Labels[labels.RemoteGeneration] = expectedGeneration
		return false
	}

	return true
}

func (r *RemoteInterface) SyncStateToRemote(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}
	if r.NativeObject.Status.State != r.RemoteObject.Status.State {
		r.RemoteObject.Status.State = r.NativeObject.Status.State
		return r.CustomClient.Status().Update(ctx, r.RemoteObject)
	}
	return nil
}

func (r *RemoteInterface) HandleNativeSpecChange(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	// ignore cases where native object has never been reconciled!
	if r.NativeObject.Status.ObservedGeneration == 0 {
		return nil
	}

	if r.NativeObject.Status.ObservedGeneration != r.RemoteObject.Generation {
		if err := r.HandleDeletingState(ctx); err != nil {
			return err
		}

		// create fresh object
		remoteObject, err := r.createRemote(ctx)
		if err != nil {
			return err
		}
		r.RemoteObject = remoteObject
	}

	return nil
}

func (r *RemoteInterface) HandleDeletingState(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	if err := r.CustomClient.Delete(ctx, r.RemoteObject); err != nil {
		return err
	}

	return nil
}

func (r *RemoteInterface) RemoveFinalizerOnRemote(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	controllerutil.RemoveFinalizer(r.RemoteObject, labels.ManifestFinalizer)
	if err := r.CustomClient.Update(ctx, r.RemoteObject); err != nil {
		return err
	}
	// only set to nil after removing finalizer
	r.RemoteObject = nil
	return nil
}
