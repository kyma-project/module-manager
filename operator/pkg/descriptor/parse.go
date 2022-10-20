package descriptor

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/kyma-project/module-manager/operator/pkg/types"

	"github.com/google/go-containerregistry/pkg/crane"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/kyma-project/module-manager/operator/pkg/util"

	yaml2 "sigs.k8s.io/yaml"
)

func GetPathFromExtractedTarGz(imageSpec types.ImageSpec, insecureRegistry bool) (string, error) {
	imageRef := fmt.Sprintf("%s/%s@%s", imageSpec.Repo, imageSpec.Name, imageSpec.Ref)

	// check existing dir
	// if dir exists return existing dir
	installPath := util.GetFsChartPath(imageSpec)
	dir, err := os.Open(installPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("opening dir for installs caused an error %s: %w", imageRef, err)
	}
	if dir != nil {
		return installPath, nil
	}

	// pull image layer
	var layer v1.Layer
	if insecureRegistry {
		layer, err = crane.PullLayer(imageRef, crane.Insecure)
	} else {
		layer, err = crane.PullLayer(imageRef)
	}
	if err != nil {
		return "", err
	}

	// uncompress chart to install path
	blobReadCloser, err := layer.Compressed()
	if err != nil {
		return "", fmt.Errorf("fetching blob resulted in an error %s: %w", imageRef, err)
	}
	uncompressedStream, err := gzip.NewReader(blobReadCloser)
	if err != nil {
		return "", fmt.Errorf("failure in NewReader() while extracting TarGz %s: %w", imageRef, err)
	}
	tarReader := tar.NewReader(uncompressedStream)
	return installPath, writeTarGzContent(installPath, tarReader, imageRef)
}

func writeTarGzContent(installPath string, tarReader *tar.Reader, layerReference string) error {
	// create dir for uncompressed chart
	if err := os.MkdirAll(installPath, util.OwnerFilePermission); err != nil {
		return fmt.Errorf("failure in MkdirAll() while extracting TarGz %s: %w", layerReference, err)
	}

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("failed Next() while extracting TarGz %s: %w", layerReference, err)
		}

		destinationPath, err := util.CleanFilePathJoin(installPath, header.Name)
		if err != nil {
			return err
		}
		if err = handleExtractedHeaderFile(header, tarReader, destinationPath, layerReference); err != nil {
			return err
		}
	}
	return nil
}

func handleExtractedHeaderFile(header *tar.Header, reader io.Reader, destinationPath string, layerReference string,
) error {
	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(destinationPath, util.OthersReadExecuteFilePermission); err != nil {
			return fmt.Errorf("failure in Mkdir() storage while extracting TarGz %s: %w", layerReference, err)
		}
	case tar.TypeReg:
		//nolint:nosnakecase
		outFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
		if err != nil {
			return fmt.Errorf("file create failed while extracting TarGz %s: %w", layerReference, err)
		}
		if _, err := io.Copy(outFile, reader); err != nil {
			return fmt.Errorf("file copy storage failed while extracting TarGz %s: %w", layerReference, err)
		}
		return outFile.Close()
	default:
		return fmt.Errorf("unknown type encountered while extracting TarGz %v in %s",
			header.Typeflag, destinationPath)
	}
	return nil
}

func DecodeYamlFromDigest(config types.ImageSpec) (interface{}, error) {
	filePath := util.GetConfigFilePath(config)
	imageRef := fmt.Sprintf("%s/%s@%s", config.Repo, config.Name, config.Ref)

	// check existing file
	decodedConfig, err := util.GetYamlFileContent(filePath)
	if err == nil {
		return decodedConfig, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("opening file for install config caused an error %s: %w", imageRef, err)
	}

	// proceed only if file was not found
	// yaml is not compressed
	layer, err := crane.PullLayer(imageRef)
	if err != nil {
		return nil, err
	}
	blob, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("fetching blob resulted in an error %s: %w", layer, err)
	}

	return writeYamlContent(blob, imageRef, filePath)
}

func writeYamlContent(blob io.ReadCloser, layerReference string, filePath string) (interface{}, error) {
	var decodedConfig interface{}
	err := yaml.NewYAMLOrJSONDecoder(blob, util.YamlDecodeBufferSize).Decode(&decodedConfig)
	if err != nil {
		return nil, fmt.Errorf("yaml blob decoding resulted in an error %s: %w", layerReference, err)
	}

	bytes, err := yaml2.Marshal(decodedConfig)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal for install config caused an error %s: %w", layerReference, err)
	}

	// close file
	return decodedConfig, util.WriteToFile(filePath, bytes)
}
