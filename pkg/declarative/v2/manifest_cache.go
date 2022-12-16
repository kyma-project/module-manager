package v2

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
)

const (
	manifest = "manifest"
)

type manifestCache struct {
	root string
	file string
	hash string
}

func newManifestCache(baseDir string, spec *Spec) *manifestCache {
	root := filepath.Join(baseDir, manifest, spec.Path)
	file := filepath.Join(root, spec.ManifestName)
	hashedValues, _ := util.CalculateHash(spec.Values)
	hash := fmt.Sprintf("%v", hashedValues)

	return &manifestCache{
		root: root,
		file: fmt.Sprintf("%s-%s.yaml", file, hash),
		hash: fmt.Sprintf("%v", hashedValues),
	}
}

func (c *manifestCache) String() string {
	return c.file
}

func (c *manifestCache) Clean() error {
	removeAllOld := func(path string, info fs.FileInfo, err error) error {
		if info == nil || info.IsDir() {
			return nil
		}
		oldFile := filepath.Join(c.root, info.Name())
		if oldFile != c.file {
			return os.Remove(oldFile)
		}
		return nil
	}
	return filepath.Walk(c.root, removeAllOld)
}

func (c *manifestCache) ReadYAML() *types.ParsedFile {
	return types.NewParsedFile(util.GetStringifiedYamlFromFilePath(c.String()))
}
