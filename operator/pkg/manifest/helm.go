package manifest

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"

	"github.com/kyma-project/module-manager/operator/pkg/resource"
	manifestRest "github.com/kyma-project/module-manager/operator/pkg/rest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type helm struct {
	deployInfo   types.InstallInfo
	logger       *logr.Logger
	actionClient *action.Install
	repoHandler  *RepoHandler
	helmClient   *HelmClient
	manifestTransformer
}

func NewHelmProcessor(deployInfo types.InstallInfo, logger *logr.Logger, actionClient *action.Install) (*helm, error) {
	settings := cli.New()
	restGetter := manifestRest.NewRESTClientGetter(deployInfo.Config)
	kubeClient := kube.New(restGetter)
	helmClient, err := NewHelmClient(kubeClient, restGetter, deployInfo.Config, settings)
	if err != nil {
		return nil, err
	}
	return &helm{
		repoHandler:         NewRepoHandler(logger, settings),
		logger:              logger,
		deployInfo:          deployInfo,
		actionClient:        actionClient,
		manifestTransformer: manifestTransformer{},
		helmClient:          helmClient,
	}, nil
}

func (h *helm) processManifest(transforms []types.ObjectTransform,
) (string, error) {
	var err error
	chartPath := h.deployInfo.ChartPath

	if chartPath == "" {
		if err := h.repoHandler.Add(h.deployInfo.RepoName, h.deployInfo.URL, h.logger); err != nil {
			return "", err
		}
		// legacy case - remote helm repo
		chartPath, err = h.helmClient.DownloadChart(h.actionClient, h.deployInfo.ChartName)
		if err != nil {
			return "", err
		}
	}

	h.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if rendered manifest doesn't exist
	chartRequested, err := h.repoHandler.LoadChart(chartPath, h.actionClient)
	if err != nil {
		return "", err
	}

	// retrieve manifest
	release, err := h.actionClient.Run(chartRequested, h.deployInfo.Flags.SetFlags)
	if err != nil {
		return "", err
	}

	return release.Manifest, nil
}

func (h *helm) handleRenderedManifestForStaticChart(chartName, chartPath string) (string, error) {
	// verify chart path exists
	if _, err := os.Stat(chartPath); err != nil {
		return "", fmt.Errorf("locating chart %s at path %s resulted in an error: %w", chartName, chartPath, err)
	}
	h.logger.Info(fmt.Sprintf("chart dir %s found at path %s", chartName, chartPath))

	// check if rendered manifest already exists
	stringifiedManifest, err := resource.GetStringifiedYamlFromFilePath(util.GetFsManifestChartPath(chartPath))
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("locating chart rendered manifest %s at path %s resulted in an error: %w",
				chartName, chartPath, err)
		}
		return "", nil
	}

	// return already rendered manifest here
	return stringifiedManifest, nil
}
