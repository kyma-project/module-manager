package types

import "github.com/kyma-project/module-manager/operator/pkg/types"

type ReconcileFlagConfig struct {
	Codec                   *types.Codec
	CheckReadyStates        bool
	CustomStateCheck        bool
	InsecureRegistry        bool
	MaxConcurrentReconciles int
}
