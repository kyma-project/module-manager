package manifest

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/pkg/resource"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type Rendered struct {
	logger logr.Logger
}

// NewRendered returns a new instance on Rendered.
// Using Rendered instance, pre-Rendered and cached manifest can be identified and retrieved.
//

func NewRendered(logger logr.Logger) *Rendered {
	return &Rendered{
		logger: logger,
	}
}

// GetCachedResources returns a resource manifest which was already cached during previous operations
// by the module-manager library.
func (r *Rendered) GetCachedResources(chartName, chartPath string) *types.ParsedFile {
	if emptyPath(chartPath) {
		return &types.ParsedFile{}
	}

	// verify chart path exists
	if _, err := os.Stat(chartPath); err != nil {
		return types.NewParsedFile("", err)
	}
	r.logger.Info(fmt.Sprintf("chart dir %s found at path %s", chartName, chartPath))

	// check if pre-Rendered manifest already exists
	return types.NewParsedFile(util.GetStringifiedYamlFromFilePath(util.GetFsManifestChartPath(chartPath)))
}

// GetManifestResources returns a pre-Rendered resource manifest located at the passed chartPath.
func (r *Rendered) GetManifestResources(chartName, chartPath string) *types.ParsedFile {
	if emptyPath(chartPath) {
		return &types.ParsedFile{}
	}
	// return already Rendered manifest here
	return types.NewParsedFile(resource.GetStringifiedYamlFromDirPath(chartPath, r.logger))
}

func emptyPath(dirPath string) bool {
	return dirPath == ""
}
