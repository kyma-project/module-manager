package custom

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/pkg/labels"
)

type ClusterClient struct {
	DefaultClient client.Client
}

func (cc *ClusterClient) GetRESTConfig(
	ctx context.Context, kymaOwner string, namespace string,
) (*rest.Config, error) {
	kubeConfigSecretList := &v1.SecretList{}
	groupResource := v1.SchemeGroupVersion.WithResource(string(v1.ResourceSecrets)).GroupResource()
	labelSelector := k8slabels.SelectorFromSet(k8slabels.Set{labels.KymaName: kymaOwner})
	err := cc.DefaultClient.List(
		ctx, kubeConfigSecretList, &client.ListOptions{LabelSelector: labelSelector, Namespace: namespace},
	)
	if err != nil {
		return nil, err
	}
	kubeConfigSecret := &v1.Secret{}
	if len(kubeConfigSecretList.Items) < 1 {
		key := client.ObjectKey{Name: kymaOwner, Namespace: namespace}
		if err := cc.DefaultClient.Get(ctx, key, kubeConfigSecret); err != nil {
			return nil, fmt.Errorf("could not get secret (containing kubeconfig) by key (%s) or selector (%s)",
				key, labelSelector.String())
		}
	} else {
		kubeConfigSecret = &kubeConfigSecretList.Items[0]
	}
	if len(kubeConfigSecretList.Items) > 1 {
		return nil, errors.NewConflict(groupResource, kymaOwner, fmt.Errorf("more than one instance found"))
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigSecret.Data["config"])
	if err != nil {
		return nil, err
	}
	return restConfig, err
}
