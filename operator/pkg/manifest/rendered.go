package manifest

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/pkg/resource"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type rendered struct {
	logger *logr.Logger
}

func (r *rendered) GetCachedResources(chartName, chartPath string) (string, error) {
	// verify chart path exists
	if _, err := os.Stat(chartPath); err != nil {
		return "", fmt.Errorf("locating chart %s at path %s resulted in an error: %w", chartName, chartPath, err)
	}
	r.logger.Info(fmt.Sprintf("chart dir %s found at path %s", chartName, chartPath))

	// check if rendered manifest already exists
	stringifiedManifest, err := util.GetStringifiedYamlFromFilePath(util.GetFsManifestChartPath(chartPath))
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("locating chart rendered manifest %s at path %s resulted in an error: %w",
				chartName, chartPath, err)
		}
	}

	// return already rendered manifest here
	return stringifiedManifest, nil
}

func (r *rendered) GetManifestResources(chartName, chartPath string) (string, error) {
	stringifiedManifest, err := resource.GetStringifiedYamlFromDirPath(chartPath, r.logger)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("searching for manifest %s at path %s resulted in an error: %w",
			chartName, chartPath, err)
	}

	// return already rendered manifest here
	return stringifiedManifest, nil
}
