package manifest

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/kyma-project/module-manager/operator/pkg/applier"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type kustomize struct {
	logger  *logr.Logger
	applier *applier.SetApplier
	*transformer
	*rendered
}

func NewKustomizeProcessor(dynamicClient dynamic.Interface, discoveryMapper *restmapper.DeferredDiscoveryRESTMapper,
	logger *logr.Logger, render *rendered, txformer *transformer,
) (types.RenderSrc, error) {

	// TODO offer SSA as a generic installation and not only bound to Kustomize
	ssaApplier := applier.NewSSAApplier(dynamicClient, logger, discoveryMapper)

	// verify compliance of interface
	var kustomizeProcessor types.RenderSrc = &kustomize{
		logger:      logger,
		transformer: txformer,
		rendered:    render,
		applier:     ssaApplier,
	}

	return kustomizeProcessor, nil
}

func (k *kustomize) GetRawManifest(deployInfo types.InstallInfo) (string, error) {
	opts := krusty.MakeDefaultOptions()
	kustomizer := krusty.MakeKustomizer(opts)
	fileSystem := filesys.MakeFsOnDisk()
	//os.ReadDir()
	path := deployInfo.URL
	if path == "" {
		path = deployInfo.ChartPath
	}
	resMap, err := kustomizer.Run(fileSystem, path)
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

	return manifestStringified, nil
}

func (k *kustomize) Install(manifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform) (bool, error) {
	// transform
	objects, err := k.Transform(deployInfo.Ctx, manifest, deployInfo.BaseResource, transforms)
	if err != nil {
		return false, err
	}

	// TODO fill namespace from user options
	if err = k.applier.Apply(deployInfo, objects, ""); err != nil {
		return false, err
	}

	return true, nil
}

func (k *kustomize) Uninstall(manifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform) (bool, error) {
	// transform
	objects, err := k.Transform(deployInfo.Ctx, manifest, deployInfo.BaseResource, transforms)
	if err != nil {
		return false, err
	}
	// TODO fill namespace from user options
	deletionSuccess, err := k.applier.Delete(deployInfo, objects, "")
	if err != nil {
		return false, err
	}

	return deletionSuccess, nil
}

func (k *kustomize) IsConsistent(manifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform) (bool, error) {
	// TODO evaluate a better consistency check
	return k.Install(manifest, deployInfo, transforms)
}
