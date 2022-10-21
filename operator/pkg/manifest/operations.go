package manifest

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kyma-project/module-manager/operator/pkg/applier"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)

type renderSrc interface {
	processManifest([]types.ObjectTransform) (string, error)
	transform(context.Context, string, types.BaseCustomObject, []types.ObjectTransform) error
}

type Operations struct {
	logger             *logr.Logger
	kubeClient         *kube.Client
	helmClient         *HelmClient
	repoHandler        *RepoHandler
	restGetter         *manifestRest.ManifestRESTClientGetter
	actionClient       *action.Install
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
	renderSrc          renderSrc
}

func InstallChart(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.install(deployInfo)
}

func UninstallChart(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.uninstall(deployInfo)
}

func ConsistencyCheck(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.consistencyCheck(deployInfo)
}

func newOperations(logger *logr.Logger, deployInfo types.InstallInfo, resourceTransforms []types.ObjectTransform,
) (*Operations, error) {
	settings := cli.New()
	restGetter := manifestRest.NewRESTClientGetter(deployInfo.Config)
	kubeClient := kube.New(restGetter)
	helmClient, err := NewHelmClient(kubeClient, restGetter, deployInfo.Config, settings)
	if err != nil {
		return nil, err
	}

	operations := &Operations{
		logger:             logger,
		restGetter:         restGetter,
		repoHandler:        NewRepoHandler(logger, settings),
		kubeClient:         kubeClient,
		helmClient:         helmClient,
		flags:              deployInfo.Flags,
		resourceTransforms: resourceTransforms,
	}

	operations.actionClient, err = operations.helmClient.NewInstallActionClient(v1.NamespaceDefault,
		deployInfo.ReleaseName, deployInfo.Flags)
	if err != nil {
		return nil, err
	}

	operations.renderSrc, err = NewHelmProcessor(deployInfo, logger, operations.actionClient)
	if err != nil {
		return nil, err
	}
	if deployInfo.Kustomize {
		operations.renderSrc = NewKustomizeProcessor(deployInfo, logger)
	}
	return operations, nil
}

func (o *Operations) getClusterResources(deployInfo types.InstallInfo, operation HelmOperation) (types.ResourceLists, error) {
	manifest, err := o.getManifestForChartPath(deployInfo)
	if err != nil {
		return types.ResourceLists{}, err
	}

	NsResourceList, err := o.helmClient.GetNsResource(o.actionClient, operation)
	if err != nil {
		return types.ResourceLists{}, err
	}

	targetResourceList, err := o.helmClient.GetTargetResources(deployInfo.Ctx, manifest,
		o.actionClient.Namespace, o.resourceTransforms, deployInfo.BaseResource)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render resources from manifest: %w", err)
	}

	existingResourceList, err := util.FilterExistingResources(targetResourceList)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render existing resources from manifest: %w", err)
	}

	return types.ResourceLists{
		Target:    targetResourceList,
		Installed: existingResourceList,
		Namespace: NsResourceList,
	}, nil
}

func (o *Operations) consistencyCheck(deployInfo types.InstallInfo) (bool, error) {
	// verify CRDs
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		false); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// verify CR
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, deployInfo.ClusterInfo.Client,
		false); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// verify manifest resources - by count
	// TODO: better strategy for resource verification?
	resourceLists, err := o.getClusterResources(deployInfo, "")
	if err != nil {
		return false, errors.Wrap(err, "could not render current resources from manifest")
	}
	if len(resourceLists.Target) > len(resourceLists.Installed) {
		return false, nil
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *Operations) install(deployInfo types.InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		true); err != nil {
		return false, err
	}

	// Kustomize
	if deployInfo.Kustomize {
		stringifiedManifest, err := o.renderSrc.processManifest(o.resourceTransforms)
		if err != nil {
			return false, err
		}
		ssaApplier := &applier.SetApplier{
			PatchOptions: v1.PatchOptions{FieldManager: "manifest-lib"},
		}
		objects, err := util.ParseManifestStringToObjects(stringifiedManifest)
		if err != nil {
			return false, err
		}
		return true, ssaApplier.Apply(deployInfo, objects, "")
	}

	// Helm - default
	resourceLists, err := o.getClusterResources(deployInfo, OperationCreate)
	if err != nil {
		return false, err
	}

	if _, err = o.installResources(resourceLists); err != nil {
		return false, err
	}

	if ready, err := o.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, OperationCreate); !ready || err != nil {
		return ready, err
	}

	o.logger.Info("Install Complete!! Happy Manifesting!", "release", deployInfo.ReleaseName,
		"chart", deployInfo.ChartName)

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(deployInfo.Ctx); err != nil {
		return false, err
	}

	// install crs - if present do not update!
	if err := resource.CheckCRs(deployInfo.Ctx, deployInfo.CustomResources, deployInfo.Client,
		true); err != nil {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, nil
}

func (o *Operations) installResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	// create namespace resource first
	if len(resourceLists.Namespace) > 0 {
		if err := o.helmClient.CreateNamespace(resourceLists.Namespace); err != nil {
			return nil, err
		}
	}

	if resourceLists.Installed == nil && len(resourceLists.Target) > 0 {
		return o.helmClient.PerformCreate(resourceLists)
	}

	return o.helmClient.PerformUpdate(resourceLists, true)
}

func (o *Operations) verifyResources(ctx context.Context, resourceLists types.ResourceLists, verifyReadyStates bool,
	operationType HelmOperation,
) (bool, error) {
	// if Wait or WaitForJobs is enabled, wait for resources to be ready with a timeout
	if err := o.helmClient.CheckWaitForResources(resourceLists.GetWaitForResources(), o.actionClient,
		operationType); err != nil {
		return false, err
	}

	if verifyReadyStates {
		// check target resources are ready or deleted without waiting
		return o.helmClient.CheckDesiredState(ctx, resourceLists.GetWaitForResources(), operationType)
	}

	return true, nil
}

func (o *Operations) uninstallResources(resourceLists types.ResourceLists) error {
	if resourceLists.Installed != nil {
		response, delErrors := o.kubeClient.Delete(resourceLists.Installed)
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err := range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return wrappedError
		}

		o.logger.Info("component deletion executed", "resource count", len(response.Deleted))
	}

	if len(resourceLists.Namespace) > 0 {
		return o.helmClient.DeleteNamespace(resourceLists.Namespace)
	}
	return nil
}

func (o *Operations) uninstall(deployInfo types.InstallInfo) (bool, error) {
	resourceLists, err := o.getClusterResources(deployInfo, OperationDelete)
	if err != nil {
		return false, err
	}

	// delete crs first - proceed only if not found
	// proceed if CR type doesn't exist anymore - since associated CRDs might be deleted from resource uninstallation
	// since there might be a deletion process to be completed by other manifest resources
	deleted, err := resource.RemoveCRs(deployInfo.Ctx, deployInfo.CustomResources,
		deployInfo.ClusterInfo.Client)
	if !meta.IsNoMatchError(err) && (err != nil || !deleted) {
		return false, err
	}

	if err := o.uninstallResources(resourceLists); err != nil {
		return false, err
	}

	if ready, err := o.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, OperationDelete); !ready || err != nil {
		return ready, err
	}

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(deployInfo.Ctx); err != nil {
		return false, err
	}

	// delete crds first - if not present ignore!
	if err := resource.RemoveCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client); err != nil {
		return false, err
	}

	// custom states check
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.ClusterInfo)
	}
	return true, err
}

func (o *Operations) getManifestForChartPath(deployInfo types.InstallInfo) (string, error) {
	// 1. check pre-rendered manifests
	renderedManifest, err := o.handleRenderedManifestForStaticChart(deployInfo.ChartName, deployInfo.ChartPath)
	if err != nil {
		return "", err
	} else if renderedManifest != "" {
		return renderedManifest, nil
	}

	// 2. render manifests
	renderedManifest, err = o.renderSrc.processManifest(o.resourceTransforms)
	if err != nil {
		return "", err
	}

	// 3. persist only if static chart path was passed
	if deployInfo.ChartPath != "" {
		err = util.WriteToFile(util.GetFsManifestChartPath(deployInfo.ChartPath), []byte(renderedManifest))
	}

	// optional: Uncomment below to print manifest
	// fmt.Println(release.Manifest)

	return renderedManifest, err
}

func (o *Operations) handleRenderedManifestForStaticChart(chartName, chartPath string) (string, error) {
	if chartPath == "" {
		return "", nil
	}

	// verify chart path exists
	if _, err := os.Stat(chartPath); err != nil {
		return "", fmt.Errorf("locating chart %s at path %s resulted in an error: %w", chartName, chartPath, err)
	}
	o.logger.Info(fmt.Sprintf("chart dir %s found at path %s", chartName, chartPath))

	// 1. check for cached manifest
	stringifiedManifest, err := resource.GetStringifiedYamlFromFilePath(util.GetFsManifestChartPath(chartPath))
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("locating chart rendered manifest %s at path %s resulted in an error: %w",
				chartName, chartPath, err)
		}
	} else if stringifiedManifest != "" {
		return stringifiedManifest, nil
	}

	// 2. Read arbitrary manifest yaml file
	return resource.GetStringifiedYamlFromDirPath(chartPath, o.logger)
}
