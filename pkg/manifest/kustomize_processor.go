package manifest

import (
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/kyma-project/module-manager/pkg/applier"
	manifestClient "github.com/kyma-project/module-manager/pkg/client"
	"github.com/kyma-project/module-manager/pkg/util"

	"github.com/kyma-project/module-manager/pkg/types"
)

type kustomize struct {
	clients *manifestClient.SingletonClients
	logger  logr.Logger
	applier *applier.SetApplier
	*Rendered
}

// NewKustomizeProcessor returns a new instance of the kustomize processor.
// The returned kustomize instance contains necessary clients based on rest config and rest mapper.
// Additionally, it also transforms the manifest resources based on user defined input.
// On the returned helm instance, installation, uninstallation and verification checks
// can then be executed on the resource manifest.
func NewKustomizeProcessor(
	clients *manifestClient.SingletonClients, logger logr.Logger, render *Rendered,
) (types.ManifestClient, error) {
	// TODO offer SSA as a generic installation and not only bound to Kustomize
	ssaApplier := applier.NewSSAApplier(clients, logger)

	// verify compliance of interface
	var kustomizeProcessor types.ManifestClient = &kustomize{
		clients:  clients,
		logger:   logger,
		Rendered: render,
		applier:  ssaApplier,
	}

	return kustomizeProcessor, nil
}

// GetRawManifest returns processed resource manifest using kustomize client.
func (k *kustomize) GetRawManifest(deployInfo *types.InstallInfo) *types.ParsedFile {
	opts := krusty.MakeDefaultOptions()
	kustomizer := krusty.MakeKustomizer(opts)

	// file system on which kustomize works on
	fileSystem := filesys.MakeFsOnDisk()
	path := deployInfo.URL
	if path == "" {
		path = deployInfo.ChartPath
	}
	resMap, err := kustomizer.Run(fileSystem, path)
	if err != nil {
		k.logger.Error(err, "running kustomize to create final manifest")
		return types.NewParsedFile("", fmt.Errorf("error running kustomize: %w", err))
	}

	var manifestStringified string
	manifestYaml, err := resMap.AsYaml()
	if err != nil {
		k.logger.Error(err, "creating final manifest yaml")
		err = fmt.Errorf("error converting kustomize output to yaml: %w", err)
	} else {
		manifestStringified = string(manifestYaml)
	}

	return types.NewParsedFile(manifestStringified, err)
}

// Install transforms and applies the kustomize manifest using server side apply.
func (k *kustomize) Install(manifest string, deployInfo *types.InstallInfo,
	transforms []types.ObjectTransform, _ []types.PostRun,
) (bool, error) {
	// transform
	objects, err := util.Transform(deployInfo.Ctx, manifest, deployInfo.BaseResource, transforms)
	if err != nil {
		return false, err
	}

	// TODO fill namespace from user options
	return k.applier.Apply(deployInfo, objects, "")
}

// Uninstall transforms and deletes kustomize based manifest using dynamic client.
func (k *kustomize) Uninstall(manifest string, deployInfo *types.InstallInfo,
	transforms []types.ObjectTransform, _ []types.PostRun,
) (bool, error) {
	// transform
	objects, err := util.Transform(deployInfo.Ctx, manifest, deployInfo.BaseResource, transforms)
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

// IsConsistent indicates if kustomize installation is consistent with the desired manifest resources.
func (k *kustomize) IsConsistent(manifest string, deployInfo *types.InstallInfo,
	transforms []types.ObjectTransform, postRuns []types.PostRun,
) (bool, error) {
	// TODO evaluate a better consistency check
	return k.Install(manifest, deployInfo, transforms, postRuns)
}

func (k *kustomize) InvalidateConfigAndRenderedManifest(_ *types.InstallInfo, _ uint32) (uint32, error) {
	// TODO implement invalidation logic
	return 0, nil
}

func (k *kustomize) GetClusterInfo() (types.ClusterInfo, error) {
	restConfig, err := k.clients.ToRESTConfig()
	if err != nil {
		return types.ClusterInfo{}, err
	}
	return types.ClusterInfo{
		Client: k.clients,
		Config: restConfig,
	}, nil
}
