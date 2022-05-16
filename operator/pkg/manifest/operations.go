package manifest

import (
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Operations struct {
	logger      logr.Logger
	kubeClient  *kube.Client
	helmClient  *HelmClient
	repoHandler *RepoHandler
}

func NewOperations(logger logr.Logger, kubeClient *kube.Client, settings *cli.EnvSettings) *Operations {
	return &Operations{
		logger:      logger,
		kubeClient:  kubeClient,
		helmClient:  NewClient(kubeClient, settings),
		repoHandler: NewRepoHandler(logger, settings),
	}
}

func (o *Operations) Install(chartPath, releaseName, chartName, repoName, url string, args map[string]string) error {
	if err := o.repoHandler.Add(repoName, url); err != nil {
		return err
	}

	actionClient, err := o.helmClient.NewInstallActionClient(v1.NamespaceDefault, releaseName, args)
	if err != nil {
		return err
	}

	manifest, err := o.getManifestForChartPath(chartPath, chartName, actionClient, args)
	if err != nil {
		return err
	}

	if err = o.helmClient.HandleNamespace(actionClient, OperationCreate); err != nil {
		return err
	}

	targetResources, err := o.helmClient.GetTargetResources(manifest)
	if err != nil {
		return err
	}

	existingResources, err := util.FilterExistingResources(targetResources)
	if err != nil {
		return errors.Wrap(err, "could not render current resources from manifest")
	}

	if existingResources == nil && len(targetResources) > 0 {
		if _, err = o.helmClient.PerformCreate(targetResources); err != nil {
			return err
		}
	} else {
		if _, err = o.helmClient.PerformUpdate(existingResources, targetResources, true); err != nil {
			return err
		}
	}

	o.logger.Info("Install Complete!! Happy Manifesting!", "release", releaseName, "chart", chartName)

	// update manifest chart in a separate go-routine
	return o.repoHandler.Update()
}

func (o *Operations) Uninstall(chartPath, chartName, releaseName string, args map[string]string) error {
	actionClient, err := o.helmClient.NewInstallActionClient(v1.NamespaceDefault, releaseName, args)
	if err != nil {
		return err
	}

	manifest, err := o.getManifestForChartPath(chartPath, chartName, actionClient, args)
	if err != nil {
		return err
	}

	if err = o.helmClient.HandleNamespace(actionClient, OperationDelete); err != nil {
		return err
	}

	targetResources, err := o.helmClient.GetTargetResources(manifest)
	if err != nil {
		return err
	}

	existingResources, err := util.FilterExistingResources(targetResources)
	if err != nil {
		return errors.Wrap(err, "could not render current resources from manifest")
	}

	if existingResources != nil {
		response, delErrors := o.kubeClient.Delete(existingResources)
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err = range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return wrappedError
		}

		o.logger.Info("component deletion executed", "resource count", len(response.Deleted))
	}

	// update manifest chart in a separate go-routine
	return o.repoHandler.Update()
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
	fmt.Println(release.Manifest)
	return release.Manifest, nil
}
