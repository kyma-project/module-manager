package manifest

import (
	"context"
	"fmt"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/custom"
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

// ChartInfo defines helm chart information.
type ChartInfo struct {
	ChartPath   string
	RepoName    string
	URL         string
	ChartName   string
	ReleaseName string
	Flags       types.ChartFlags
}

type ResourceLists struct {
	Target    kube.ResourceList
	Installed kube.ResourceList
	Namespace kube.ResourceList
}

func (r ResourceLists) getWaitForResources() kube.ResourceList {
	return append(r.Target, r.Namespace...)
}

// InstallInfo represents deployment information artifacts to be processed.
type InstallInfo struct {
	// ChartInfo represents chart information to be processed
	*ChartInfo
	// ResourceInfo represents additional resources to be processed
	ResourceInfo
	// ClusterInfo represents target cluster information
	custom.ClusterInfo
	// Ctx hold the current context
	Ctx context.Context //nolint:containedctx
	// CheckFn returns a boolean indicating ready state based on custom checks
	CheckFn custom.CheckFnType
	// CheckReadyStates indicates if native resources should be checked for ready states
	CheckReadyStates bool
}

// ResourceInfo represents additional resources.
type ResourceInfo struct {
	// BaseResource represents base custom resource that is being reconciled
	BaseResource *unstructured.Unstructured
	// CustomResources represents a set of additional custom resources to be installed
	CustomResources []*unstructured.Unstructured
	// Crds represents a set of additional custom resource definitions to be installed
	Crds []*apiextensions.CustomResourceDefinition
}

type ResponseChan chan *InstallResponse

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

type Operations struct {
	logger             *logr.Logger
	kubeClient         *kube.Client
	helmClient         *HelmClient
	repoHandler        *RepoHandler
	restGetter         *manifestRest.ManifestRESTClientGetter
	actionClient       *action.Install
	flags              types.ChartFlags
	resourceTransforms []types.ObjectTransform
}

func InstallChart(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.install(deployInfo)
}

func UninstallChart(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.uninstall(deployInfo)
}

func ConsistencyCheck(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
) (bool, error) {
	operations, err := newOperations(logger, deployInfo, resourceTransforms)
	if err != nil {
		return false, err
	}

	return operations.consistencyCheck(deployInfo)
}

func newOperations(logger *logr.Logger, deployInfo InstallInfo, resourceTransforms []types.ObjectTransform,
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
	return operations, nil
}

func (o *Operations) getClusterResources(deployInfo InstallInfo, operation HelmOperation) (ResourceLists, error) {
	if deployInfo.ChartPath == "" {
		if err := o.repoHandler.Add(deployInfo.RepoName, deployInfo.URL, o.logger); err != nil {
			return ResourceLists{}, err
		}
	}

	manifest, err := o.getManifestForChartPath(deployInfo.ChartPath, deployInfo.ChartName, o.actionClient, o.flags)
	if err != nil {
		return ResourceLists{}, err
	}

	NsResourceList, err := o.helmClient.GetNsResource(o.actionClient, operation)
	if err != nil {
		return ResourceLists{}, err
	}

	targetResourceList, err := o.helmClient.GetTargetResources(deployInfo.Ctx, manifest,
		o.actionClient.Namespace, o.resourceTransforms, deployInfo.BaseResource)
	if err != nil {
		return ResourceLists{}, fmt.Errorf("could not render resources from manifest: %w", err)
	}

	existingResourceList, err := util.FilterExistingResources(targetResourceList)
	if err != nil {
		return ResourceLists{}, fmt.Errorf("could not render existing resources from manifest: %w", err)
	}

	return ResourceLists{
		Target:    targetResourceList,
		Installed: existingResourceList,
		Namespace: NsResourceList,
	}, nil
}

func (o *Operations) consistencyCheck(deployInfo InstallInfo) (bool, error) {
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

func (o *Operations) install(deployInfo InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CheckCRDs(deployInfo.Ctx, deployInfo.Crds, deployInfo.ClusterInfo.Client,
		true); err != nil {
		return false, err
	}

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

func (o *Operations) installResources(resourceLists ResourceLists) (*kube.Result, error) {
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

func (o *Operations) verifyResources(ctx context.Context, resourceLists ResourceLists, verifyReadyStates bool,
	operationType HelmOperation,
) (bool, error) {
	// if Wait or WaitForJobs is enabled, wait for resources to be ready with a timeout
	if err := o.helmClient.CheckWaitForResources(resourceLists.getWaitForResources(), o.actionClient,
		operationType); err != nil {
		return false, err
	}

	if verifyReadyStates {
		// check target resources are ready or deleted without waiting
		return o.helmClient.CheckDesiredState(ctx, resourceLists.getWaitForResources(), operationType)
	}

	return true, nil
}

func (o *Operations) uninstallResources(resourceLists ResourceLists) error {
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

func (o *Operations) uninstall(deployInfo InstallInfo) (bool, error) {
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

func (o *Operations) getManifestForChartPath(chartPath, chartName string, actionClient *action.Install,
	flags types.ChartFlags,
) (string, error) {
	var err error
	if chartPath == "" {
		chartPath, err = o.helmClient.DownloadChart(actionClient, chartName)
		if err != nil {
			return "", err
		}
	}
	o.logger.Info("chart located", "path", chartPath)

	chartRequested, err := o.repoHandler.LoadChart(chartPath, actionClient)
	if err != nil {
		return "", err
	}

	// retrieve manifest
	release, err := actionClient.Run(chartRequested, flags.SetFlags)
	if err != nil {
		return "", err
	}
	// TODO: Uncomment below to print manifest
	// fmt.Println(release.Manifest)
	return release.Manifest, nil
}
