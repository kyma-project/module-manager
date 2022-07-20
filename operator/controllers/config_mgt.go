package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/operator/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/descriptor"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func prepareDeployInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClient client.Client,
	verifyInstallation bool, customStateCheck bool, codec *v1alpha1.Codec, defaultRestConfig *rest.Config,
) ([]manifest.DeployInfo, error) {
	deployInfos := make([]manifest.DeployInfo, 0)
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	kymaOwnerLabel, ok := manifestObj.Labels[labels.ComponentOwner]
	if !ok {
		return nil, fmt.Errorf("label %s not set for manifest resource %s",
			labels.ComponentOwner, namespacedName)
	}

	// extract config
	config := manifestObj.Spec.Config

	decodedConfig, err := descriptor.DecodeYamlFromDigest(config.Repo, config.Name, config.Ref,
		filepath.Join(fmt.Sprintf("%s", config.Ref), "installConfig.yaml"))
	if err != nil {
		return nil, err
	}
	installConfigObj, ok := decodedConfig.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(configReadError)
	}
	configs, ok := installConfigObj["configs"].([]interface{})
	if !ok {
		return nil, fmt.Errorf(configReadError)
	}

	// evaluate rest config
	customResCheck := &CustomResourceCheck{DefaultClient: defaultClient}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: defaultClient}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace, defaultRestConfig)
	if err != nil {
		return nil, err
	}

	for _, install := range manifestObj.Spec.Installs {
		chartInfo, err := getChartInfoForInstall(install, codec, manifestObj)
		if err != nil {
			return nil, err
		}

		mergedConfig, mergedChartValues, err := mergeConfigsWithOverrides(install, configs)
		if err != nil {
			return nil, err
		}

		// common deploy properties
		chartInfo.ReleaseName = install.Name
		chartInfo.Overrides = mergedChartValues
		chartInfo.ClientConfig = mergedConfig

		deployInfo := manifest.DeployInfo{
			Ctx:            ctx,
			ManifestLabels: manifestObj.Labels,
			ChartInfo:      chartInfo,
			ObjectKey:      namespacedName,
			RestConfig:     restConfig,
			CheckFn:        customResCheck.CheckProcessingFn,
			ReadyCheck:     verifyInstallation,
		}
		if !customStateCheck {
			deployInfo.CheckFn = nil
		}
		deployInfos = append(deployInfos, deployInfo)
	}

	return deployInfos, nil
}

func getChartInfoForInstall(install v1alpha1.InstallInfo, codec *v1alpha1.Codec,
	manifestObj *v1alpha1.Manifest) (*manifest.ChartInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	specType, err := v1alpha1.GetSpecType(install.Source.Raw)
	if err != nil {
		return nil, err
	}

	switch specType {
	case v1alpha1.HelmChartType:
		var helmChartSpec v1alpha1.HelmChartSpec
		if err = codec.Decode(install.Source.Raw, &helmChartSpec, specType); err != nil {
			return nil, err
		}

		return &manifest.ChartInfo{
			ChartName: fmt.Sprintf("%s/%s", install.Name, helmChartSpec.ChartName),
			RepoName:  install.Name,
			Url:       helmChartSpec.Url,
		}, nil

	case v1alpha1.OciRefType:
		var imageSpec v1alpha1.ImageSpec
		if err = codec.Decode(install.Source.Raw, &imageSpec, specType); err != nil {
			return nil, err
		}

		// extract helm chart from layer digest
		chartPath, err := descriptor.ExtractTarGz(imageSpec.Repo, imageSpec.Name, imageSpec.Ref,
			fmt.Sprintf("%s-%s", install.Name, imageSpec.Ref))
		if err != nil {
			return nil, err
		}

		return &manifest.ChartInfo{
			ChartName: install.Name,
			ChartPath: chartPath,
		}, nil
	}

	return nil, fmt.Errorf("unsupported type %s of install for Manifest %s", specType, namespacedName)
}

func getConfigAndOverridesForInstall(installName string, configs []interface{}) (string, string, error) {
	var defaultOverrides string
	var clientConfig string

	for _, config := range configs {
		mappedConfig, ok := config.(map[string]interface{})
		if !ok {
			return "", "", fmt.Errorf(configReadError)
		}
		if mappedConfig["name"] == installName {
			defaultOverrides, ok = mappedConfig["overrides"].(string)
			if !ok {
				return "", "", fmt.Errorf(configReadError)
			}
			clientConfig, ok = mappedConfig["clientConfig"].(string)
			if !ok {
				return "", "", fmt.Errorf(configReadError)
			}
			break
		}
	}
	return clientConfig, defaultOverrides, nil
}

func addOwnerRefToConfigMap(
	ctx context.Context, configMap *v1.ConfigMap, manifestObj *v1alpha1.Manifest, restClient client.Client,
) error {
	// we now verify that we already own the config map
	previousOwnerRefs := len(configMap.GetOwnerReferences())
	if err := controllerutil.SetOwnerReference(manifestObj, configMap, restClient.Scheme()); err != nil {
		return fmt.Errorf("override configuration could not be owned to watch for overrides: %w", err)
	}
	if previousOwnerRefs != len(configMap.GetOwnerReferences()) {
		if err := restClient.Update(ctx, configMap); err != nil {
			return fmt.Errorf("error updating newly set owner config map: %w", err)
		}
	}
	return nil
}

func mergeConfigsWithOverrides(install v1alpha1.InstallInfo, configs []interface{}) (
	map[string]interface{}, map[string]interface{}, error) {

	configString, chartValuesString, err := getConfigAndOverridesForInstall(install.Name, configs)
	if err != nil {
		return nil, nil, err
	}

	config := map[string]interface{}{}
	if err := strvals.ParseInto(configString, config); err != nil {
		return nil, nil, err
	}
	chartValues := map[string]interface{}{}
	if err := strvals.ParseInto(chartValuesString, chartValues); err != nil {
		return nil, nil, err
	}

	return config, chartValues, nil
}
