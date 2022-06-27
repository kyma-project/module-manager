package custom

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterClient struct {
	DefaultClient client.Client
}

func (cc *ClusterClient) GetNewClient(restConfig *rest.Config, options client.Options) (client.Client, error) {
	client, err := client.New(restConfig, options)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (cc *ClusterClient) GetRestConfig(ctx context.Context, kymaOwner string, namespace string) (*rest.Config, error) {
	kubeConfigSecretList := &v1.SecretList{}
	if err := cc.DefaultClient.List(ctx, kubeConfigSecretList, &client.ListOptions{
		LabelSelector: k8slabels.SelectorFromSet(
			k8slabels.Set{labels.ComponentOwner: kymaOwner}), Namespace: namespace,
	}); err != nil {
		return nil, err
	} else if len(kubeConfigSecretList.Items) < 1 {
		gr := v1.SchemeGroupVersion.WithResource("secrets").GroupResource()
		return nil, errors.NewNotFound(gr, kymaOwner)
	} else if len(kubeConfigSecretList.Items) > 1 {
		gr := v1.SchemeGroupVersion.WithResource("secrets").GroupResource()
		return nil, errors.NewConflict(gr, kymaOwner, fmt.Errorf("more than one instance found"))
	}
	kubeConfigSecret := kubeConfigSecretList.Items[0]
	if err := cc.DefaultClient.Get(ctx, client.ObjectKey{Name: kymaOwner, Namespace: namespace}, &kubeConfigSecret); err != nil {
		return nil, err
	}

	kubeconfigString := string(kubeConfigSecret.Data["config"])
	restConfig, err := util.GetConfig(kubeconfigString, "")
	if err != nil {
		return nil, err
	}
	return restConfig, err
}
