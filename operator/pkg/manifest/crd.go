package manifest

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"path/filepath"
)

func GetCRDsFromPath(filePath string) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	pathInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var files []os.FileInfo
	if !pathInfo.IsDir() {
		filePath, files = filepath.Dir(filePath), []os.FileInfo{pathInfo}
	} else if files, err = ioutil.ReadDir(filePath); err != nil {
		return nil, err
	}

	crdsList, err := readCRDs(filePath, files)
	if err != nil {
		return nil, err
	}

	return crdsList, nil
}

func readCRDs(basePath string, files []os.FileInfo) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	var crds []*apiextensionsv1.CustomResourceDefinition

	crdExtensions := sets.NewString(".yaml")

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

		fmt.Println("read CRDs from file", "file", file.Name())
	}

	return crds, nil
}

func readFile(stringifiedFile string) ([][]byte, error) {
	fileBytes, err := ioutil.ReadFile(stringifiedFile)
	if err != nil {
		return nil, err
	}

	docs := [][]byte{}
	reader := yaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(fileBytes)))
	for {
		doc, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}

			return nil, err
		}

		docs = append(docs, doc)
	}

	return docs, nil
}
