package manifest

import (
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type kustomize struct {
	deployInfo types.InstallInfo
	logger     *logr.Logger
	manifestTransformer
}

func NewKustomizeProcessor(deployInfo types.InstallInfo, logger *logr.Logger) *kustomize {
	return &kustomize{
		deployInfo:          deployInfo,
		logger:              logger,
		manifestTransformer: manifestTransformer{},
	}
}

func (k *kustomize) processManifest(transforms []types.ObjectTransform,
) (string, error) {
	opts := krusty.MakeDefaultOptions()
	kustomizer := krusty.MakeKustomizer(opts)
	fs := filesys.MakeFsOnDisk()
	resMap, err := kustomizer.Run(fs, k.deployInfo.ChartPath)
	if err != nil {
		k.logger.Error(err, "running kustomize to create final manifest")
		return "", fmt.Errorf("error running kustomize: %v", err)
	}

	manifestYaml, err := resMap.AsYaml()
	if err != nil {
		k.logger.Error(err, "creating final manifest yaml")
		return "", fmt.Errorf("error converting kustomize output to yaml: %v", err)
	}

	manifestStringified := string(manifestYaml)

	k.transform(k.deployInfo.Ctx, manifestStringified, k.deployInfo.BaseResource, transforms)

	return manifestStringified, nil
}
