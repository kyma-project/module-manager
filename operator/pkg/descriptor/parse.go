package descriptor

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/kyma-project/manifest-operator/operator/pkg/util"
	"k8s.io/apimachinery/pkg/util/yaml"

	yaml2 "sigs.k8s.io/yaml"
)

const (
	ownerFilePermission             = 0o770
	othersReadExecuteFilePermission = 0o755
	yamlDecodeBufferSize            = 2048
)

func GetPathFromExtractedTarGz(repo string, module string, digest string, pathPattern string) (string, error) {
	reference := fmt.Sprintf("%s/%s@%s", repo, module, digest)
	layer, err := crane.PullLayer(reference)
	if err != nil {
		return "", err
	}

	// check existing dir
	installPath := filepath.Join(os.TempDir(), pathPattern)
	dir, err := os.Open(installPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("opening dir for installs caused an error %s: %w", reference, err)
	}
	if dir != nil {
		return installPath, nil
	}
	blobReadCloser, err := layer.Compressed()
	if err != nil {
		return "", fmt.Errorf("fetching blob resulted in an error %s: %w", reference, err)
	}
	uncompressedStream, err := gzip.NewReader(blobReadCloser)
	if err != nil {
		return "", fmt.Errorf("failure in NewReader() while extracting TarGz %s: %w", reference, err)
	}

	// create base dir
	if err = os.MkdirAll(installPath, ownerFilePermission); err != nil {
		return "", fmt.Errorf("failure in MkdirAll() while extracting TarGz %s: %w", layer, err)
	}

	// extract content to install path
	tarReader := tar.NewReader(uncompressedStream)
	return installPath, writeTarGzContent(installPath, tarReader, reference)
}

func writeTarGzContent(installPath string, tarReader *tar.Reader, layerReference string) error {
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
		if err := os.Mkdir(destinationPath, othersReadExecuteFilePermission); err != nil {
			return fmt.Errorf("failure in Mkdir() storage while extracting TarGz %s: %w", layerReference, err)
		}
	case tar.TypeReg:
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

func DecodeYamlFromDigest(repo string, module string, digest string, pathPattern string) (interface{}, error) {
	reference := fmt.Sprintf("%s/%s@%s", repo, module, digest)
	layer, err := crane.PullLayer(reference)
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(os.TempDir(), pathPattern)

	// check existing file
	decodedConfig, err := checkExistingYamlFile(filePath, reference)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("opening file for install config caused an error %s: %w", reference, err)
	} else if err == nil {
		return decodedConfig, nil
	}

	// proceed only if file was not found
	// yaml is not compressed
	blob, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("fetching blob resulted in an error %s: %w", layer, err)
	}

	return writeYamlContent(blob, reference, filePath)
}

func checkExistingYamlFile(filePath string, layerReference string) (interface{}, error) {
	var decodedConfig interface{}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	if file != nil {
		if err = yaml.NewYAMLOrJSONDecoder(file, yamlDecodeBufferSize).Decode(&decodedConfig); err != nil {
			return nil, fmt.Errorf("reading content for install config caused an error %s: %w", layerReference, err)
		}
		err = file.Close()
	}

	return decodedConfig, err
}

func writeYamlContent(blob io.ReadCloser, layerReference string, filePath string) (interface{}, error) {
	var decodedConfig interface{}
	err := yaml.NewYAMLOrJSONDecoder(blob, yamlDecodeBufferSize).Decode(&decodedConfig)
	if err != nil {
		return nil, fmt.Errorf("yaml blob decoding resulted in an error %s: %w", layerReference, err)
	}

	bytes, err := yaml2.Marshal(decodedConfig)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal for install config caused an error %s: %w", layerReference, err)
	}

	// create directory
	if err := os.MkdirAll(filepath.Dir(filePath), ownerFilePermission); err != nil {
		return nil, err
	}

	// create file
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("file creation for install config caused an error %s: %w", layerReference, err)
	}

	// write to file
	if _, err = file.Write(bytes); err != nil {
		return nil, fmt.Errorf("writing file for install config caused an error %s: %w", layerReference, err)
	}

	// close file
	return decodedConfig, file.Close()
}
