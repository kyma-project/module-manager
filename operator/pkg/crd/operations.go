package crd

import (
	"bufio"
	"context"
	standardErrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func GetCRDsFromPath(ctx context.Context, filePath string) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	dirEntries := make([]fs.DirEntry, 0)
	if err := filepath.WalkDir(filePath, func(path string, info fs.DirEntry, err error) error {
		// initial error
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		dirEntries, err = os.ReadDir(filePath)
		return err
	}); err != nil {
		return nil, err
	}

	crdsList, err := readCRDs(ctx, filePath, dirEntries)
	if err != nil {
		return nil, err
	}

	return crdsList, nil
}

func readCRDs(ctx context.Context, basePath string, files []os.DirEntry,
) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	var crds []*apiextensionsv1.CustomResourceDefinition
	logger := log.FromContext(ctx).WithName("CRDs")

	crdExtensions := sets.NewString(".yaml", ".json", ".yml")

	for _, file := range files {
		if !crdExtensions.Has(filepath.Ext(file.Name())) {
			continue
		}

		// Unmarshal CRDs from file into structs
		docs, err := readFile(filepath.Join(basePath, file.Name()))
		if err != nil {
			return nil, err
		}

		for _, doc := range docs {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err = yaml.Unmarshal(doc, crd); err != nil {
				return nil, err
			}

			if crd.Kind != "CustomResourceDefinition" || crd.Spec.Names.Kind == "" || crd.Spec.Group == "" {
				continue
			}
			crds = append(crds, crd)
		}

		if len(crds) < 1 {
			err = fmt.Errorf("no CRDs found in path %s", basePath)
			logger.Error(err, "")
			return nil, err
		}

		logger.Info("read CRDs from", "file", file.Name())
	}

	return crds, nil
}

func readFile(fileName string) ([][]byte, error) {
	yamlStructs := make([][]byte, 0)
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	yamlReader := yaml.NewYAMLReader(reader)

	for {
		structByte, err := yamlReader.Read()
		if standardErrors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}

		yamlStructs = append(yamlStructs, structByte)
	}

	return yamlStructs, nil
}

func CreateCRDs(ctx context.Context, crds []*apiextensionsv1.CustomResourceDefinition,
	destinationClient client.Client,
) error {
	for _, crd := range crds {
		existingCrd := apiextensionsv1.CustomResourceDefinition{}
		err := destinationClient.Get(ctx, client.ObjectKeyFromObject(crd), &existingCrd)
		if err != nil {
			if errors.IsNotFound(err) {
				if err = destinationClient.Create(ctx, crd); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	return nil
}
