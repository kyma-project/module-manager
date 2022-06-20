package manifest

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	manifestRest "github.com/kyma-project/manifest-operator/operator/pkg/rest"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

type Mode int

const (
	CreateMode Mode = iota
	DeletionMode
)

// ChartInfo defines chart information
type ChartInfo struct {
	ChartPath    string
	RepoName     string
	Url          string
	ChartName    string
	ReleaseName  string
	ClientConfig string
}

type DeployInfo struct {
	Ctx            context.Context
	ManifestLabels map[string]string
	ChartInfo
	client.ObjectKey
	RestConfig *rest.Config
	CheckFn    custom.CheckFnType
	ReadyCheck bool
}

type RequestErrChan chan *RequestError

type RequestError struct {
	Ready             bool
	ResNamespacedName client.ObjectKey
	Err               error
}

func (r *RequestError) Error() string {
	return r.Err.Error()
}

type Operations struct {
	logger      *logr.Logger
	kubeClient  *kube.Client
	helmClient  *HelmClient
	repoHandler *RepoHandler
	restGetter  *manifestRest.ManifestRESTClientGetter
}

func NewOperations(logger *logr.Logger, restConfig *rest.Config, settings *cli.EnvSettings, waitTimeout time.Duration) (*Operations, error) {
	operations := &Operations{
		logger:      logger,
		restGetter:  manifestRest.NewRESTClientGetter(restConfig),
		repoHandler: NewRepoHandler(logger, settings),
	}

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return &Operations{}, err
	}
	operations.kubeClient = kube.New(operations.restGetter)
	operations.helmClient = NewClient(operations.kubeClient, operations.restGetter, clientSet, settings, waitTimeout)
	return operations, nil
}

func (o *Operations) Install(deployInfo DeployInfo, args map[string]string) (bool, error) {
	if err := o.repoHandler.Add(deployInfo.RepoName, deployInfo.Url); err != nil {
		return false, err
	}

	actionClient, err := o.helmClient.NewInstallActionClient(v1.NamespaceDefault, deployInfo.ReleaseName, args)
	if err != nil {
		return false, err
	}

	manifest, err := o.getManifestForChartPath(deployInfo.ChartPath, deployInfo.ChartName, actionClient, args)
	if err != nil {
		return false, err
	}

	if err = o.helmClient.HandleNamespace(actionClient, OperationCreate); err != nil {
		return false, err
	}

	targetResources, err := o.helmClient.GetTargetResources(manifest, actionClient.Namespace)
	if err != nil {
		return false, err
	}

	existingResources, err := util.FilterExistingResources(targetResources)
	if err != nil {
		return false, errors.Wrap(err, "could not render current resources from manifest")
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
	if err = o.helmClient.CheckWaitForResources(targetResources, actionClient, OperationCreate); err != nil {
		return false, err
	}

	if deployInfo.ReadyCheck {
		// check target resources are ready without waiting
		if ready, err := o.helmClient.CheckReadyState(deployInfo.Ctx, targetResources); !ready || err != nil {
			return ready, err
		}
	}

	o.logger.Info("Install Complete!! Happy Manifesting!", "release", deployInfo.ReleaseName, "chart", deployInfo.ChartName)

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(); err != nil {
		return false, err
	}

	// check custom function, if provided
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.ManifestLabels, deployInfo.ObjectKey, o.logger)
	}

	return true, nil
}

func (o *Operations) Uninstall(deployInfo DeployInfo, args map[string]string) (bool, error) {
	actionClient, err := o.helmClient.NewInstallActionClient(v1.NamespaceDefault, deployInfo.ReleaseName, args)
	if err != nil {
		return false, err
	}

	manifest, err := o.getManifestForChartPath(deployInfo.ChartPath, deployInfo.ChartName, actionClient, args)
	if err != nil {
		return false, err
	}

	if err = o.helmClient.HandleNamespace(actionClient, OperationDelete); err != nil {
		return false, err
	}

	targetResources, err := o.helmClient.GetTargetResources(manifest, actionClient.Namespace)
	if err != nil {
		return false, err
	}

	existingResources, err := util.FilterExistingResources(targetResources)
	if err != nil {
		return false, errors.Wrap(err, "could not render current resources from manifest")
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

	if err = o.helmClient.CheckWaitForResources(targetResources, actionClient, OperationDelete); err != nil {
		return false, err
	}

	// update manifest chart in a separate go-routine
	if err = o.repoHandler.Update(); err != nil {
		return false, err
	}

	// check custom function, if provided
	if deployInfo.CheckFn != nil {
		return deployInfo.CheckFn(deployInfo.Ctx, deployInfo.ManifestLabels, deployInfo.ObjectKey, o.logger)
	}

	return true, nil
}

func (o *Operations) getManifestForChartPath(chartPath, chartName string, actionClient *action.Install, args map[string]string) (string, error) {
	var err error
	if chartPath == "" {
		chartPath, err = o.helmClient.DownloadChart(actionClient, chartName)
		if err != nil {
			return "", err
		}
	}
	o.logger.Info("chart located", "path", chartPath)

	// "set" args
	mergedValues := map[string]interface{}{}
	if err = strvals.ParseInto(args["set"], mergedValues); err != nil {
		return "", err
	}

	chartRequested, err := o.repoHandler.LoadChart(chartPath, actionClient)
	if err != nil {
		return "", err
	}

	// retrieve manifest
	release, err := actionClient.Run(chartRequested, mergedValues)
	if err != nil {
		return "", err
	}
	// TODO: Uncomment below to print manifest
	//fmt.Println(release.Manifest)
	return release.Manifest, nil
}
