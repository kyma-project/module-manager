package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type RemoteInterface struct {
	NativeClient client.Client
	NativeObject *v1alpha1.Manifest
	RemoteObject *v1alpha1.Manifest
}

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

	customClient, err := remoteCluster.GetNewClient(restConfig)
	if err != nil {
		return nil, err
	}

	var remoteObject *v1alpha1.Manifest
	err = customClient.Get(ctx, namespacedName, remoteObject)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}

	var update bool

	// check finalizer
	if !controllerutil.ContainsFinalizer(remoteObject, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(remoteObject, labels.ManifestFinalizer)
		update = true
	}

	// remote object doesn't exist
	if err != nil {
		remoteObject.Spec = *nativeObject.Spec.DeepCopy()
		update = true
	}

	if update {
		if err = customClient.Create(ctx, remoteObject); err != nil {
			return nil, err
		}
	}

	return &RemoteInterface{
		NativeObject: nativeObject,
		NativeClient: nativeClient,
		RemoteObject: remoteObject,
	}, nil
}

func (r *RemoteInterface) IsSynced() bool {
	remoteGeneration, ok := r.NativeObject.Labels[labels.RemoteGeneration]
	if !ok {
		// label missing
		r.NativeObject.Labels[labels.RemoteGeneration] = string(r.RemoteObject.Generation)
		return false
	}

	if remoteGeneration != string(r.RemoteObject.Generation) {
		// outdated
		r.NativeObject.Spec = *r.RemoteObject.Spec.DeepCopy()
		r.NativeObject.Labels[labels.RemoteGeneration] = string(r.RemoteObject.Generation)
		return false
	}

	return true
}
