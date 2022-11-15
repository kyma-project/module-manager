package resource

import (
	"bufio"
	"context"
	standardErrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ChartKind int

const (
	HelmKind ChartKind = iota
	KustomizeKind
	UnknownKind
)

func getDirContent(filePath string) ([]fs.DirEntry, error) {
	dirEntries := make([]fs.DirEntry, 0)
	err := filepath.WalkDir(filePath, func(path string, info fs.DirEntry, err error) error {
		// initial error
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		dirEntries, err = os.ReadDir(filePath)
		return err
	})

	return dirEntries, err
}

func GetChartKind(deployInfo types.InstallInfo) (ChartKind, error) {
	// URLs are not verified at this state
	if deployInfo.URL != "" {
		// URL without RepoName is expected for Kustomize
		if deployInfo.RepoName == "" {
			return KustomizeKind, nil
		}
		// RepoName + URL is only set for Helm
		return HelmKind, nil
	}

	kind := UnknownKind

	// traverse directory content if local chart path is specified
	fileEntries, err := getDirContent(deployInfo.ChartPath)
	if err != nil {
		return UnknownKind, err
	}

	for _, entry := range fileEntries {
		if entry.Name() == "kustomization.yaml" {
			return KustomizeKind, nil
		} else if entry.Name() == "Chart.yaml" {
			return HelmKind, nil
		}
	}

	return kind, nil
}

func GetStringifiedYamlFromDirPath(dirPath string, logger logr.Logger) (string, error) {
	dirEntries, err := getDirContent(dirPath)
	if err != nil {
		return "", err
	}

	childCount := len(dirEntries)
	if childCount == 0 {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("no yaml file found at file path %s", dirPath))
		return "", nil
	} else if childCount > 1 {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("more than onw yaml file found at file path %s", dirPath))
		return "", nil
	}
	file := dirEntries[0]
	allowedExtns := sets.NewString(".yaml", ".yml")
	if !allowedExtns.Has(filepath.Ext(file.Name())) {
		return "", fmt.Errorf("file extension unsupported %s in dir %s", file.Name(), dirPath)
	}

	stringifiedYaml, err := util.GetStringifiedYamlFromFilePath(filepath.Join(dirPath, file.Name()))
	if err != nil {
		return "", fmt.Errorf("yaml file could not be read %s in dir %s: %w", file.Name(), dirPath, err)
	}
	return stringifiedYaml, nil
}

func GetCRDsFromPath(ctx context.Context, filePath string) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	dirEntries, err := getDirContent(filePath)
	if err != nil {
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

		logger.V(util.DebugLogLevel).Info("read CRDs from", "file", file.Name())
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

func CheckCRDs(ctx context.Context, crds []*apiextensionsv1.CustomResourceDefinition,
	destinationClient client.Client, createIfNotPresent bool,
) error {
	for _, crd := range crds {
		existingCrd := apiextensionsv1.CustomResourceDefinition{}
		if err := destinationClient.Get(ctx, client.ObjectKeyFromObject(crd), &existingCrd); err != nil {
			if !createIfNotPresent || client.IgnoreNotFound(err) != nil {
				return err
			}

			if err = destinationClient.Create(ctx, crd); err != nil {
				return err
			}
		}
	}
	return nil
}

func CheckCRs(ctx context.Context, crs []*unstructured.Unstructured,
	destinationClient client.Client, createIfNotPresent bool,
) error {
	for _, resource := range crs {
		existingCustomResource := unstructured.Unstructured{}
		existingCustomResource.SetGroupVersionKind(resource.GroupVersionKind())
		customResourceKey := client.ObjectKey{
			Name:      resource.GetName(),
			Namespace: resource.GetNamespace(),
		}
		if err := destinationClient.Get(ctx, customResourceKey, &existingCustomResource); err != nil {
			if !createIfNotPresent || client.IgnoreNotFound(err) != nil {
				return err
			}

			if err = destinationClient.Create(ctx, resource); err != nil {
				return err
			}
		}
	}
	return nil
}

func RemoveCRDs(ctx context.Context, crds []*apiextensionsv1.CustomResourceDefinition,
	destinationClient client.Client,
) error {
	for _, crd := range crds {
		existingCrd := apiextensionsv1.CustomResourceDefinition{}
		if err := destinationClient.Get(ctx, client.ObjectKeyFromObject(crd), &existingCrd); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return err
			}
			continue
		}

		if err := destinationClient.Delete(ctx, &existingCrd); err != nil {
			return err
		}
	}
	return nil
}

func RemoveCRs(ctx context.Context, crs []*unstructured.Unstructured,
	destinationClient client.Client,
) (bool, error) {
	deleted := true
	for _, resource := range crs {
		existingCustomResource := unstructured.Unstructured{}
		existingCustomResource.SetGroupVersionKind(resource.GroupVersionKind())
		customResourceKey := client.ObjectKey{
			Name:      resource.GetName(),
			Namespace: resource.GetNamespace(),
		}
		if err := destinationClient.Get(ctx, customResourceKey, &existingCustomResource); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return false, err
			}
			// resource deleted
			continue
		}

		deleted = false
		if err := destinationClient.Delete(ctx, resource); err != nil {
			return false, err
		}
	}
	return deleted, nil
}
