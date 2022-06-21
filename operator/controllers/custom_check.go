package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CustomResourceCheck struct {
	DefaultClient client.Client
	custom.Check
}

func (c *CustomResourceCheck) CheckProcessingFn(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey, logger *logr.Logger) (bool, error) {
	kymaOwnerLabel, ok := manifestLabels[labels.ComponentOwner]
	if !ok {
		err := fmt.Errorf("label %s not set for manifest resource %s", labels.ComponentOwner, namespacedName)
		logger.Error(err, "")
		return false, err
	}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: c.DefaultClient}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, namespacedName.Namespace)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while evaluating rest config for manifest resource %s", namespacedName))
		return false, err
	}

	customClient, err := clusterClient.GetNewClient(restConfig)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while evaluating target client for manifest resource %s", namespacedName))
		return false, err
	}

	// check custom resource for states
	customStatus := &custom.Status{
		Reader: customClient,
	}

	manifestObj := v1alpha1.Manifest{}
	if err = c.DefaultClient.Get(ctx, namespacedName, &manifestObj); err != nil {
		return false, err
	}

	ready, err := customStatus.WaitForCustomResources(ctx, manifestObj.Spec.CustomStates)
	if err != nil {
		logger.Error(err, fmt.Sprintf("error while tracking status of custom resources for manifest resource %s", namespacedName))
		return false, err
	}

	return ready, nil
}

func (c *CustomResourceCheck) CheckReadyFn(ctx context.Context, manifestLabels map[string]string, namespacedName client.ObjectKey, logger *logr.Logger) (bool, error) {
	return true, nil
}
