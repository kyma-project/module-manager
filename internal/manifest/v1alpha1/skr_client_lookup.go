package v1alpha1

import (
	"context"

	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/internal"
	"github.com/kyma-project/module-manager/pkg/custom"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/client-go/rest"
)

type RESTConfigGetter func() (*rest.Config, error)

type RemoteClusterLookup struct {
	KCP          *types.ClusterInfo
	ConfigGetter RESTConfigGetter
}

func (r *RemoteClusterLookup) ConfigResolver(ctx context.Context, obj declarative.Object) (*types.ClusterInfo, error) {
	manifest := obj.(*v1alpha1.Manifest)
	// in single cluster mode return the default cluster info
	// since the resources need to be installed in the same cluster
	if !manifest.Spec.Remote {
		return r.KCP, nil
	}

	kymaOwnerLabel, err := internal.GetResourceLabel(manifest, labels.KymaName)
	if err != nil {
		return nil, err
	}

	// RESTConfig can either be retrieved by a secret with name contained in labels.KymaName Manifest CR label,
	// or it can be retrieved as a function return value, passed during controller startup.
	var restConfigGetter RESTConfigGetter
	if r.ConfigGetter != nil {
		restConfigGetter = r.ConfigGetter
	} else {
		restConfigGetter = func() (*rest.Config, error) {
			// evaluate remote rest config from secret
			return (&custom.ClusterClient{DefaultClient: r.KCP.Client}).GetRESTConfig(
				ctx, kymaOwnerLabel, manifest.GetNamespace(),
			)
		}
	}

	config, err := restConfigGetter()
	if err != nil {
		return nil, err
	}

	return &types.ClusterInfo{Config: config}, nil
}
