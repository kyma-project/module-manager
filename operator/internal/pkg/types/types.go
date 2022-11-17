package types

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// ReconcileFlagConfig describes configurable flag properties for the controller.
type ReconcileFlagConfig struct {
	Codec                   *types.Codec
	CheckReadyStates        bool
	CustomStateCheck        bool
	InsecureRegistry        bool
	MaxConcurrentReconciles int
	InstallTargetSrc        string
}

type ResponseChan chan *InstallResponse

// InstallResponse holds information describing the installation response of the Manifest by workers.
//
//nolint:errname
type InstallResponse struct {
	Ready             bool
	ChartName         string
	Flags             types.ChartFlags
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *InstallResponse) Error() string {
	return r.Err.Error()
}

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)
