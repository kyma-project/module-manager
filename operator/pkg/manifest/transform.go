package manifest

import (
	"context"

	"k8s.io/client-go/rest"

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type Transformer struct {
	config *rest.Config
}

func NewTransformer(config *rest.Config) *Transformer {
	return &Transformer{
		config: config,
	}
}

func (t *Transformer) Transform(ctx context.Context, manifestStringified string,
	object manifestTypes.BaseCustomObject, transforms []manifestTypes.ObjectTransform,
) (*manifestTypes.ManifestResources, error) {
	objects, err := util.ParseManifestStringToObjects(manifestStringified)
	if err != nil {
		return nil, err
	}

	for _, transform := range transforms {
		if err = transform(ctx, object, objects); err != nil {
			return nil, err
		}
	}

	return objects, nil
}

func (t *Transformer) GetRestConfig() *rest.Config {
	return t.config
}
