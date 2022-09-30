package prepare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	manifestCustom "github.com/kyma-project/module-manager/operator/internal/pkg/custom"
	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/descriptor"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

const (
	configReadError = "reading install %s resulted in an error for " + v1alpha1.ManifestKind
	configFileName  = "installConfig.yaml"
)

func GetInstallInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClusterInfo custom.ClusterInfo,
	flags internalTypes.ReconcileFlagConfig,
) ([]manifest.InstallInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// extract config
	config := manifestObj.Spec.Config

	var configs []any
	if config.Type.NotEmpty() { //nolint:nestif
		decodedConfig, err := descriptor.DecodeYamlFromDigest(config.Repo, config.Name, config.Ref,
			filepath.Join(config.Ref, configFileName))
		if err != nil {
			// if EOF error proceed without config
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, err
			}
		} else {
			var err error
			configs, err = parseInstallConfigs(decodedConfig)
			if err != nil {
				return nil, fmt.Errorf("manifest %s encountered an err: %w", namespacedName, err)
			}
		}
	}

	// evaluate rest config
	customResCheck := &manifestCustom.Resource{DefaultClient: defaultClusterInfo.Client}

	// check crds - if present do not update
	crds, err := parseCrds(ctx, manifestObj.Spec.CRDs, flags.InsecureRegistry)
	if err != nil {
		return nil, err
	}

	manifestObjMetadata, err := runtime.DefaultUnstructuredConverter.ToUnstructured(manifestObj)
	if err != nil {
		return nil, err
	}

	// evaluate rest config
	clusterInfo, err := getDestinationConfigAndClient(ctx, defaultClusterInfo, manifestObj)
	if err != nil {
		return nil, err
	}

	// ensure runtime-watcher labels are set to CustomResource
	InsertWatcherLabels(manifestObj)

	// parse installs
	baseDeployInfo := manifest.InstallInfo{
		ClusterInfo: clusterInfo,
		ResourceInfo: manifest.ResourceInfo{
			Crds:            crds,
			BaseResource:    &unstructured.Unstructured{Object: manifestObjMetadata},
			CustomResources: []*unstructured.Unstructured{},
		},
		Ctx:              ctx,
		CheckFn:          customResCheck.DefaultFn,
		CheckReadyStates: flags.CheckReadyStates,
	}

	// replace with check function that checks for readiness of custom resources
	if flags.CustomStateCheck {
		baseDeployInfo.CheckFn = customResCheck.CheckFn
	}

	// add custom resource if provided
	if manifestObj.Spec.Resource.Object != nil {
		baseDeployInfo.CustomResources = append(baseDeployInfo.CustomResources, &manifestObj.Spec.Resource)
	}

	return parseInstallations(manifestObj, flags.Codec, configs, baseDeployInfo, flags.InsecureRegistry)
}

func getDestinationConfigAndClient(ctx context.Context, defaultClusterInfo custom.ClusterInfo,
	manifestObj *v1alpha1.Manifest,
) (custom.ClusterInfo, error) {
	// single cluster mode
	if !manifestObj.Spec.Remote {
		return defaultClusterInfo, nil
	}

	namespacedName := client.ObjectKeyFromObject(manifestObj)
	kymaOwnerLabel, labelExists := manifestObj.Labels[labels.ComponentOwner]
	if !labelExists {
		return custom.ClusterInfo{}, fmt.Errorf("label %s not set for manifest resource %s",
			labels.ComponentOwner, namespacedName)
	}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: defaultClusterInfo.Client}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace)
	if err != nil {
		return custom.ClusterInfo{}, err
	}

	destinationClient, err := clusterClient.GetNewClient(restConfig, client.Options{})
	if err != nil {
		return custom.ClusterInfo{}, err
	}

	return custom.ClusterInfo{
		Config: restConfig,
		Client: destinationClient,
	}, nil
}

func parseInstallConfigs(decodedConfig interface{}) ([]interface{}, error) {
	var configs []interface{}
	installConfigObj, decodeOk := decodedConfig.(map[string]interface{})
	if !decodeOk {
		return nil, fmt.Errorf(configReadError, ".spec.config")
	}
	if installConfigObj["configs"] != nil {
		var configOk bool
		configs, configOk = installConfigObj["configs"].([]interface{})
		if !configOk {
			return nil, fmt.Errorf(configReadError, "chart config object of .spec.config")
		}
	}
	return configs, nil
}

func parseInstallations(manifestObj *v1alpha1.Manifest, codec *types.Codec,
	configs []interface{}, baseDeployInfo manifest.InstallInfo, insecureRegistry bool,
) ([]manifest.InstallInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	deployInfos := make([]manifest.InstallInfo, 0)

	for _, install := range manifestObj.Spec.Installs {
		deployInfo := baseDeployInfo

		// retrieve chart info
		chartInfo, err := getChartInfoForInstall(install, codec, manifestObj, insecureRegistry)
		if err != nil {
			return nil, err
		}

		// filter config for install
		chartConfig, chartValues, err := parseChartConfigAndValues(install, configs, namespacedName.String())
		if err != nil {
			return nil, err
		}

		// common deploy properties
		chartInfo.ReleaseName = install.Name
		chartInfo.Overrides = chartValues
		chartInfo.ClientConfig = chartConfig

		deployInfo.ChartInfo = chartInfo
		deployInfos = append(deployInfos, deployInfo)
	}

	return deployInfos, nil
}

func parseCrds(ctx context.Context, crdImage types.ImageSpec, insecureRegistry bool,
) ([]*v1.CustomResourceDefinition, error) {
	// if crds do not exist - do nothing
	if crdImage.Type.NotEmpty() {
		// extract helm chart from layer digest
		crdsPath, err := descriptor.GetPathFromExtractedTarGz(crdImage, insecureRegistry)
		if err != nil {
			return nil, err
		}

		return resource.GetCRDsFromPath(ctx, crdsPath)
	}
	return nil, nil
}

func getChartInfoForInstall(install v1alpha1.InstallInfo, codec *types.Codec,
	manifestObj *v1alpha1.Manifest, insecureRegistry bool,
) (*manifest.ChartInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	specType, err := types.GetSpecType(install.Source.Raw)
	if err != nil {
		return nil, err
	}

	switch specType {
	case types.HelmChartType:
		var helmChartSpec types.HelmChartSpec
		if err = codec.Decode(install.Source.Raw, &helmChartSpec, specType); err != nil {
			return nil, err
		}

		return &manifest.ChartInfo{
			ChartName: fmt.Sprintf("%s/%s", install.Name, helmChartSpec.ChartName),
			RepoName:  install.Name,
			URL:       helmChartSpec.URL,
		}, nil

	case types.OciRefType:
		var imageSpec types.ImageSpec
		if err = codec.Decode(install.Source.Raw, &imageSpec, specType); err != nil {
			return nil, err
		}

		// extract helm chart from layer digest
		chartPath, err := descriptor.GetPathFromExtractedTarGz(imageSpec, insecureRegistry)
		if err != nil {
			return nil, err
		}

		return &manifest.ChartInfo{
			ChartName: install.Name,
			ChartPath: chartPath,
		}, nil
	case types.NilRefType:
		return nil, fmt.Errorf("empty image type for %s resource chart installation", namespacedName.String())
	}

	return nil, fmt.Errorf("unsupported type %s of install for Manifest %s", specType, namespacedName)
}

func getConfigAndValuesForInstall(installName string, configs []interface{}) (
	string, string, error,
) {
	var defaultOverrides string
	var clientConfig string

	for _, config := range configs {
		mappedConfig, configExists := config.(map[string]interface{})
		if !configExists {
			return "", "", fmt.Errorf(configReadError, "config object")
		}
		if mappedConfig["name"] == installName {
			defaultOverrides, configExists = mappedConfig["overrides"].(string)
			if !configExists {
				return "", "", fmt.Errorf(configReadError, "config object overrides")
			}
			clientConfig, configExists = mappedConfig["clientConfig"].(string)
			if !configExists {
				return "", "", fmt.Errorf(configReadError, "chart config")
			}
			break
		}
	}
	return clientConfig, defaultOverrides, nil
}

func parseChartConfigAndValues(install v1alpha1.InstallInfo, configs []interface{},
	namespacedName string) (
	map[string]interface{}, map[string]interface{}, error,
) {
	configString, valuesString, err := getConfigAndValuesForInstall(install.Name, configs)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest %s encountered an error while parsing chart config: %w", namespacedName, err)
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

// InsertWatcherLabels adds watcher labels to custom resource of the Manifest CR.
func InsertWatcherLabels(manifestObj *v1alpha1.Manifest) {
	if manifestObj.Spec.Remote {
		manifestLabels := manifestObj.Spec.Resource.GetLabels()

		ownedByValue := fmt.Sprintf(labels.OwnedByFormat, manifestObj.Namespace, manifestObj.Name)

		if manifestLabels == nil {
			manifestLabels = make(map[string]string)
		}

		manifestLabels[labels.OwnedByLabel] = ownedByValue
		manifestLabels[labels.WatchedByLabel] = labels.OperatorName

		manifestObj.Spec.Resource.SetLabels(manifestLabels)
	}
}
