package manifest

import (
	"context"

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type manifestTransformer struct{}

func (m *manifestTransformer) transform(ctx context.Context, manifestStringified string,
	object manifestTypes.BaseCustomObject, transforms []manifestTypes.ObjectTransform) error {
	objects, err := util.ParseManifestStringToObjects(manifestStringified)
	if err != nil {
		return err
	}

	for _, transform := range transforms {
		if err = transform(ctx, object, objects); err != nil {
			return err
		}
	}

	return nil
}
