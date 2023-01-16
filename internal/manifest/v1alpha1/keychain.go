package v1alpha1

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	authnK8s "github.com/google/go-containerregistry/pkg/authn/kubernetes"
	"github.com/kyma-project/module-manager/pkg/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetAuthnKeychain(
	ctx context.Context,
	spec types.ImageSpec,
	clnt client.Client,
	namespace string,
) (authn.Keychain, error) {
	secretList, err := getCredSecrets(ctx, spec.CredSecretSelector, clnt, namespace)
	if err != nil {
		return nil, err
	}
	return authnK8s.NewFromPullSecrets(ctx, secretList.Items)
}

func getCredSecrets(
	ctx context.Context,
	credSecretSelector *metav1.LabelSelector,
	clusterClient client.Client,
	namespace string,
) (corev1.SecretList, error) {
	secretList := corev1.SecretList{}
	selector, err := metav1.LabelSelectorAsSelector(credSecretSelector)
	if err != nil {
		return secretList, fmt.Errorf("error converting labelSelector: %w", err)
	}
	err = clusterClient.List(
		ctx, &secretList, &client.ListOptions{
			LabelSelector: selector,
			Namespace:     namespace,
		},
	)
	if err != nil {
		return secretList, err
	}
	if len(secretList.Items) == 0 {
		return secretList, ErrNoAuthSecretFound
	}
	return secretList, nil
}
