package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"strings"

	manifestv1alpha1 "github.com/kyma-project/module-manager/api/v1alpha1"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const customResourceStatePath = "status.state"

var ErrCustomResourceNotFound = errors.New("custom resource not found")

// NewManifestCustomResourceReadyCheck creates a readiness check that verifies that the Resource in the Manifest
// returns the ready state, if not it returns not ready.
func NewManifestCustomResourceReadyCheck() *ManifestCustomResourceReadyCheck {
	return &ManifestCustomResourceReadyCheck{}
}

type ManifestCustomResourceReadyCheck struct{}

func (c *ManifestCustomResourceReadyCheck) Run(
	ctx context.Context, clnt declarative.Client, obj declarative.Object, _ []*resource.Info,
) error {
	manifest := obj.(*manifestv1alpha1.Manifest)
	res := manifest.Spec.Resource.DeepCopy()
	if err := clnt.Get(ctx, client.ObjectKeyFromObject(res), res); err != nil {
		return err
	}
	state, found, err := unstructured.NestedString(res.Object, strings.Split(customResourceStatePath, ".")...)
	if err != nil {
		return fmt.Errorf(
			"could not get state from custom resource %s at path %s",
			res.GetName(), customResourceStatePath,
		)
	}
	if !found {
		return ErrCustomResourceNotFound
	}

	if state := declarative.State(state); state != declarative.StateReady {
		return fmt.Errorf(
			"custom resource state is %s but expected %s: %w", state, declarative.StateReady,
			declarative.ErrResourcesNotReady,
		)
	}

	return nil
}
