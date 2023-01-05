package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/internal"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"github.com/kyma-project/module-manager/pkg/types"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
	"helm.sh/helm/v3/pkg/strvals"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrNoAuthSecretFound = errors.New("no auth secret found")

type ManifestSpecResolver struct {
	KCP client.Client

	*types.Codec
	Insecure bool

	ChartCache   string
	cachedCharts map[string]string
}

func NewManifestSpecResolver(codec *types.Codec, insecure bool) *ManifestSpecResolver {
	return &ManifestSpecResolver{
		Codec:        codec,
		Insecure:     insecure,
		ChartCache:   os.TempDir(),
		cachedCharts: make(map[string]string),
	}
}

func (m *ManifestSpecResolver) Spec(ctx context.Context, obj declarative.Object) (*declarative.Spec, error) {
	manifest, ok := obj.(*v1alpha1.Manifest)
	if !ok {
		return nil, fmt.Errorf(
			"spec resolver can only resolve v1alpha1 Manifests, but was given %s", reflect.TypeOf(obj),
		)
	}

	if len(manifest.Spec.Installs) != 1 {
		return nil, fmt.Errorf("%v installs found in manifest, cannot install", len(manifest.Spec.Installs))
	}

	install := manifest.Spec.Installs[0]

	specType, err := types.GetSpecType(install.Source.Raw)
	if err != nil {
		return nil, err
	}

	keyChain, err := m.lookupKeyChain(ctx, manifest.Spec.Config, obj.GetNamespace())
	if err != nil {
		return nil, err
	}

	chartInfo, err := m.getChartInfoForInstall(ctx, install, specType, keyChain)
	if err != nil {
		return nil, err
	}

	var mode declarative.RenderMode
	switch specType {
	case types.HelmChartType:
		mode = declarative.RenderModeHelm
	case types.OciRefType:
		mode = declarative.RenderModeHelm
	case types.KustomizeType:
		mode = declarative.RenderModeKustomize
	case types.NilRefType:
		return nil, fmt.Errorf("could not determine render mode for %s", client.ObjectKeyFromObject(manifest))
	}

	values, err := m.getValuesFromConfig(ctx, manifest.Spec.Config, install.Name, keyChain)
	if err != nil {
		return nil, err
	}

	path := chartInfo.ChartPath
	if path == "" && chartInfo.URL != "" {
		path = chartInfo.URL

		if mode == declarative.RenderModeHelm {
			path, err = m.downloadAndCacheHelmChart(chartInfo)
			if err != nil {
				return nil, err
			}
		}
	}

	return &declarative.Spec{
		ManifestName: install.Name,
		Path:         path,
		Values:       values,
		Mode:         mode,
	}, nil
}

func (m *ManifestSpecResolver) downloadAndCacheHelmChart(chartInfo *types.ChartInfo) (string, error) {
	filename := filepath.Join(m.ChartCache, chartInfo.ChartName)

	if cachedChart, ok := m.cachedCharts[filename]; !ok {
		getters := getter.All(cli.New())
		chart, err := repo.FindChartInRepoURL(
			chartInfo.URL,
			chartInfo.ChartName, "", "", "", "", getters,
		)
		if err != nil {
			return "", err
		}
		cachedChart, _, err := (&downloader.ChartDownloader{Getters: getters}).DownloadTo(
			chart, "", m.ChartCache,
		)
		if err != nil {
			return "", err
		}
		m.cachedCharts[filename] = cachedChart
		filename = cachedChart
	} else {
		filename = cachedChart
	}

	return filename, nil
}

func (m *ManifestSpecResolver) getValuesFromConfig(
	ctx context.Context, config types.ImageSpec, name string, keyChain authn.Keychain,
) (map[string]any, error) {
	var configs []any
	if config.Type.NotEmpty() { //nolint:nestif
		decodedConfig, err := internal.DecodeUncompressedYAMLLayer(ctx, config, m.Insecure, keyChain, name)
		if err != nil {
			// if EOF error, we should proceed without config
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
		} else {
			var err error
			configs, err = parseInstallConfigs(decodedConfig)
			if err != nil {
				return nil, fmt.Errorf("value parsing for %s encountered an err: %w", name, err)
			}
		}
	}

	// filter config for install
	chartConfig, chartValues, err := parseChartConfigAndValues(configs, name)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, len(chartConfig)+len(chartValues))
	for k, v := range chartConfig {
		values[k] = v
	}
	for k, v := range chartValues {
		values[k] = v
	}

	return values, nil
}

func parseInstallConfigs(decodedConfig interface{}) ([]interface{}, error) {
	var configs []interface{}
	installConfigObj, decodeOk := decodedConfig.(map[string]interface{})
	if !decodeOk {
		return nil, fmt.Errorf("reading install %s resulted in an error for "+v1alpha1.ManifestKind, ".spec.config")
	}
	if installConfigObj["configs"] != nil {
		var configOk bool
		configs, configOk = installConfigObj["configs"].([]interface{})
		if !configOk {
			return nil, fmt.Errorf(
				"reading install %s resulted in an error for "+v1alpha1.ManifestKind,
				"chart config object of .spec.config",
			)
		}
	}
	return configs, nil
}

func (m *ManifestSpecResolver) getChartInfoForInstall(
	ctx context.Context,
	install v1alpha1.InstallInfo,
	specType types.RefTypeMetadata,
	keyChain authn.Keychain,
) (*types.ChartInfo, error) {
	var err error
	switch specType {
	case types.HelmChartType:
		var helmChartSpec types.HelmChartSpec
		if err = m.Codec.Decode(install.Source.Raw, &helmChartSpec, specType); err != nil {
			return nil, err
		}

		return &types.ChartInfo{
			ChartName: helmChartSpec.ChartName,
			RepoName:  install.Name,
			URL:       helmChartSpec.URL,
		}, nil
	case types.OciRefType:
		var imageSpec types.ImageSpec
		if err = m.Codec.Decode(install.Source.Raw, &imageSpec, specType); err != nil {
			return nil, err
		}

		// extract helm chart from layer digest
		chartPath, err := internal.GetPathFromExtractedTarGz(ctx, imageSpec, m.Insecure, keyChain)
		if err != nil {
			return nil, err
		}

		return &types.ChartInfo{
			ChartName: install.Name,
			ChartPath: chartPath,
		}, nil
	case types.KustomizeType:
		var kustomizeSpec types.KustomizeSpec
		if err = m.Codec.Decode(install.Source.Raw, &kustomizeSpec, specType); err != nil {
			return nil, err
		}

		return &types.ChartInfo{
			ChartName: install.Name,
			ChartPath: kustomizeSpec.Path,
			URL:       kustomizeSpec.URL,
		}, nil
	case types.NilRefType:
		return nil, fmt.Errorf("empty image type")
	}

	return nil, fmt.Errorf(
		"unsupported type %s of install", specType,
	)
}

func parseChartConfigAndValues(
	configs []interface{}, name string,
) (map[string]interface{}, map[string]interface{}, error) {
	configString, valuesString, err := getConfigAndValuesForInstall(configs, name)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest encountered an error while parsing chart config: %w", err)
	}

	config := map[string]interface{}{}
	if err := strvals.ParseInto(configString, config); err != nil {
		return nil, nil, err
	}
	values := map[string]interface{}{}
	if err := strvals.ParseInto(valuesString, values); err != nil {
		return nil, nil, err
	}

	return config, values, nil
}

func getConfigAndValuesForInstall(configs []interface{}, name string) (
	string, string, error,
) {
	var defaultOverrides string
	var clientConfig string

	for _, config := range configs {
		mappedConfig, configExists := config.(map[string]interface{})
		if !configExists {
			return "", "", fmt.Errorf(
				"reading install %s resulted in an error for "+v1alpha1.ManifestKind, "config object",
			)
		}
		if mappedConfig["name"] == name {
			defaultOverrides, configExists = mappedConfig["overrides"].(string)
			if !configExists {
				return "", "", fmt.Errorf(
					"reading install %s resulted in an error for "+v1alpha1.ManifestKind, "config object overrides",
				)
			}
			clientConfig, configExists = mappedConfig["clientConfig"].(string)
			if !configExists {
				return "", "", fmt.Errorf(
					"reading install %s resulted in an error for "+v1alpha1.ManifestKind, "chart config",
				)
			}
			break
		}
	}
	return clientConfig, defaultOverrides, nil
}

func (m *ManifestSpecResolver) lookupKeyChain(
	ctx context.Context,
	imageSpec types.ImageSpec,
	namespace string,
) (authn.Keychain, error) {
	var keyChain authn.Keychain
	var err error
	if imageSpec.CredSecretSelector != nil {
		if keyChain, err = GetAuthnKeychain(ctx, imageSpec, m.KCP, namespace); err != nil {
			return nil, err
		}
	} else {
		keyChain = authn.DefaultKeychain
	}
	return keyChain, nil
}
