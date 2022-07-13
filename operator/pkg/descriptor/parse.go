package descriptor

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/crane"

	"k8s.io/apimachinery/pkg/util/yaml"

	yaml2 "sigs.k8s.io/yaml"
)

type ManifestConfig struct {
	name string
}

func ExtractTarGz(repo string, module string, digest string, pathPattern string) (string, error) {
	reference := fmt.Sprintf("%s/%s@%s", repo, module, digest)
	layer, err := crane.PullLayer(reference)
	if err != nil {
		return "", err
	}

	// check existing dir
	installPath := filepath.Join(os.TempDir(), pathPattern)
	dir, err := os.Open(installPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("opening dir for installs caused an error %s: %w", layer, err)
	}
	if dir != nil {
		return installPath, nil
	}

	blobReadCloser, err := layer.Compressed()
	if err != nil {
		return "", fmt.Errorf("fetching blob resulted in an error %s: %w", layer, err)
	}

	uncompressedStream, err := gzip.NewReader(blobReadCloser)
	if err != nil {
		return "", fmt.Errorf("failure in NewReader() while extracting TarGz %s: %w", layer, err)
	}

	// create new dir
	if err = os.MkdirAll(installPath, 0o770); err != nil {
		return "", fmt.Errorf("failure in MkdirAll() while extracting TarGz %s: %w", layer, err)
	}

	// extract content to dir path
	tarReader := tar.NewReader(uncompressedStream)
	for true {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return "", fmt.Errorf("failed Next() while extracting TarGz %s: %w", layer, err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(filepath.Join(installPath, header.Name), 0o755); err != nil {
				return "", fmt.Errorf("failure in Mkdir() storage while extracting TarGz %s: %w", layer, err)
			}
		case tar.TypeReg:
			outFile, err := os.Create(filepath.Join(installPath, header.Name))
			if err != nil {
				return "", fmt.Errorf("file create failed while extracting TarGz %s: %w", layer, err)
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				return "", fmt.Errorf("file copy storage failed while extracting TarGz %s: %w", layer, err)
			}
			outFile.Close()

		default:
			return "", fmt.Errorf("unknown type encountered while extracting TarGz %v in %s: %w",
				header.Typeflag, filepath.Join(installPath, header.Name), err)
		}
	}

	return installPath, nil
}

func DecodeYamlFromDigest(repo string, module string, digest string, pathPattern string) (interface{}, error) {
	var configDecoder interface{}
	reference := fmt.Sprintf("%s/%s@%s", repo, module, digest)
	layer, err := crane.PullLayer(reference)
	if err != nil {
		return nil, err
	}

	fullFilePath := filepath.Join(os.TempDir(), pathPattern)
	// check existing file
	file, err := os.Open(fullFilePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("opening file for install config caused an error %s: %w", layer, err)
	}
	if file != nil {
		if err = yaml.NewYAMLOrJSONDecoder(file, 2048).Decode(&configDecoder); err != nil {
			return nil, fmt.Errorf("reading content for install config caused an error %s: %w", layer, err)
		}
		return configDecoder, nil
	}

	// yaml is not compressed
	blob, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("fetching blob resulted in an error %s: %w", layer, err)
	}

	err = yaml.NewYAMLOrJSONDecoder(blob, 2048).Decode(&configDecoder)

	bytes, err := yaml2.Marshal(configDecoder)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal for install config caused an error %s: %w", layer, err)
	}

	// create directory
	if err := os.MkdirAll(filepath.Dir(fullFilePath), 0o770); err != nil {
		return nil, err
	}

	// create file
	file, err = os.Create(fullFilePath)
	if err != nil {
		return nil, fmt.Errorf("file creation for install config caused an error %s: %w", layer, err)
	}

	// write to file
	if _, err = file.Write(bytes); err != nil {
		return nil, fmt.Errorf("writing file for install config caused an error %s: %w", layer, err)
	}

	// close file
	file.Close()

	return configDecoder, err
}
