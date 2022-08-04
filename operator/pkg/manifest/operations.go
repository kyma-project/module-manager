package manifest

import (
	"context"
	"fmt"

	"github.com/kyma-project/manifest-operator/operator/pkg/resource"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	manifestRest "github.com/kyma-project/manifest-operator/operator/pkg/rest"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)

// ChartInfo defines chart information.
type ChartInfo struct {
	ChartPath    string
	RepoName     string
	URL          string
	ChartName    string
	ReleaseName  string
	ClientConfig map[string]interface{}
	Overrides    map[string]interface{}
}

// TODO: move Ctx out of struct.
type InstallInfo struct {
	*ChartInfo
	ResourceInfo
	custom.RemoteInfo
	Ctx              context.Context //nolint:containedctx
	CheckFn          custom.CheckFnType
	CheckReadyStates bool
}

type ResourceInfo struct {
	BaseResource    *unstructured.Unstructured
	CustomResources []*unstructured.Unstructured
	Crds            []*apiextensions.CustomResourceDefinition
}

type ResponseChan chan *InstallResponse

// nolint:errname
type InstallResponse struct {
	Ready             bool
	ChartName         string
	ClientConfig      map[string]interface{}
	Overrides         map[string]interface{}
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *InstallResponse) Error() string {
	return r.Err.Error()
}

type Operations struct {
	logger       *logr.Logger
	kubeClient   *kube.Client
	helmClient   *HelmClient
	repoHandler  *RepoHandler
	restGetter   *manifestRest.ManifestRESTClientGetter
	actionClient *action.Install
	args         map[string]map[string]interface{}
}

func NewOperations(logger *logr.Logger, restConfig *rest.Config, releaseName string, settings *cli.EnvSettings,
	args map[string]map[string]interface{},
) (*Operations, error) {
	restGetter := manifestRest.NewRESTClientGetter(restConfig)
	kubeClient := kube.New(restGetter)
	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return &Operations{}, err
	}

	operations := &Operations{
		logger:      logger,
		restGetter:  restGetter,
		repoHandler: NewRepoHandler(logger, settings),
		kubeClient:  kubeClient,
		helmClient:  NewHelmClient(kubeClient, restGetter, clientSet, settings),
		args:        args,
	}

	operations.actionClient, err = operations.helmClient.NewInstallActionClient(v1.NamespaceDefault, releaseName, args)
	if err != nil {
		return &Operations{}, err
	}
	return operations, nil
}

func (o *Operations) getClusterResources(deployInfo InstallInfo, operation HelmOperation) (kube.ResourceList,
	kube.ResourceList, error,
) {
	if deployInfo.ChartPath == "" {
		if err := o.repoHandler.Add(deployInfo.RepoName, deployInfo.URL, o.logger); err != nil {
			return nil, nil, err
		}
	}

	manifest, err := o.getManifestForChartPath(deployInfo.ChartPath, deployInfo.ChartName, o.actionClient, o.args)
	if err != nil {
		return nil, nil, err
	}

	if err = o.helmClient.HandleNamespace(o.actionClient, operation); err != nil {
		return nil, nil, err
	}

	targetResources, err := o.helmClient.GetTargetResources(manifest, o.actionClient.Namespace)
	if err != nil {
		return nil, nil, err
	}

	existingResources, err := util.FilterExistingResources(targetResources)
	if err != nil {
		return nil, nil, errors.Wrap(err, "could not render current resources from manifest")
	}

	return targetResources, existingResources, nil
}

func (o *Operations) VerifyResources(deployInfo InstallInfo) (bool, error) {
	targetResources, existingResources, err := o.getClusterResources(deployInfo, "")
	if err != nil {
		return false, errors.Wrap(err, "could not render current resources from manifest")
	}
	if len(targetResources) > len(existingResources) {
		return false, nil
	}
	return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.RemoteInfo)
}

func (o *Operations) Install(deployInfo InstallInfo) (bool, error) {
	// install crds first - if present do not update!
	if err := resource.CreateCRDs(deployInfo.Ctx, deployInfo.Crds, *deployInfo.RemoteClient); err != nil {
		return false, err
	}

	// install crs - if present do not update!
	if err := resource.CreateCRs(deployInfo.Ctx, deployInfo.CustomResources, *deployInfo.RemoteClient); err != nil {
		return false, err
	}

	targetResources, existingResources, err := o.getClusterResources(deployInfo, OperationCreate)
	if err != nil {
		return false, err
	}

	if existingResources == nil && len(targetResources) > 0 {
		if _, err = o.helmClient.PerformCreate(targetResources); err != nil {
			return false, err
		}
	} else {
		if _, err = o.helmClient.PerformUpdate(existingResources, targetResources, true); err != nil {
			return false, err
		}
	}

	// if Wait or WaitForJobs is enabled, wait for resources to be ready with a timeout
	if err = o.helmClient.CheckWaitForResources(targetResources, o.actionClient, OperationCreate); err != nil {
		return false, err
	}

	if deployInfo.CheckReadyStates {
		// check target resources are ready without waiting
		if ready, err := o.helmClient.CheckReadyState(deployInfo.Ctx, targetResources); !ready || err != nil {
			return ready, err
		}
	}

	o.logger.Info("Install Complete!! Happy Manifesting!", "release", deployInfo.ReleaseName,
		"chart", deployInfo.ChartName)

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(); err != nil {
		return false, err
	}

	// custom states check
	return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.RemoteInfo)
}

func (o *Operations) Uninstall(deployInfo InstallInfo) (bool, error) {
	targetResources, existingResources, err := o.getClusterResources(deployInfo, OperationDelete)
	if err != nil {
		return false, err
	}

	if existingResources != nil {
		response, delErrors := o.kubeClient.Delete(existingResources)
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err = range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return false, wrappedError
		}

		o.logger.Info("component deletion executed", "resource count", len(response.Deleted))
	}

	if err = o.helmClient.CheckWaitForResources(targetResources, o.actionClient, OperationDelete); err != nil {
		return false, err
	}

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(); err != nil {
		return false, err
	}

	// delete crs first - if not present ignore!
	if err := resource.RemoveCRs(deployInfo.Ctx, deployInfo.CustomResources, *deployInfo.RemoteClient); err != nil {
		return false, err
	}

	// delete crds first - if not present ignore!
	if err := resource.RemoveCRDs(deployInfo.Ctx, deployInfo.Crds, *deployInfo.RemoteClient); err != nil {
		return false, err
	}

	// custom states check
	return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.BaseResource, o.logger, deployInfo.RemoteInfo)
}

func (o *Operations) getManifestForChartPath(chartPath, chartName string, actionClient *action.Install,
	args map[string]map[string]interface{},
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

	mergedVals, ok := args["set"]
	if !ok {
		mergedVals = map[string]interface{}{}
	}

	// retrieve manifest
	release, err := actionClient.Run(chartRequested, mergedVals)
	if err != nil {
		return "", err
	}
	// TODO: Uncomment below to print manifest
	// fmt.Println(release.Manifest)
	return release.Manifest, nil
}
