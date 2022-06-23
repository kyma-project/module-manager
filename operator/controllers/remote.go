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

	//remoteObject := &unstructured.Unstructured{}
	//remoteObject.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind(v1alpha1.ManifestKind))
	remoteObject := &v1alpha1.Manifest{}
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
		remoteObject.Name = nativeObject.Name
		remoteObject.Namespace = nativeObject.Namespace
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
		CustomClient: customClient,
	}, nil
}

func (r *RemoteInterface) IsNativeSynced() bool {
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

func (r *RemoteInterface) SyncRemoteState(ctx context.Context) error {
	if r.NativeObject.Status.State != r.RemoteObject.Status.State {
		r.RemoteObject.Status.State = r.NativeObject.Status.State
		return r.CustomClient.Status().Update(ctx, r.RemoteObject)
	}
	return nil
}
