package controllers

import (
	"context"
	"fmt"
	"github.com/imdario/mergo"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/descriptor"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

func prepareDeployInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClient client.Client,
	verifyInstallation bool, customStateCheck bool, codec *v1alpha1.Codec,
) ([]manifest.DeployInfo, error) {
	deployInfos := make([]manifest.DeployInfo, 0)
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	kymaOwnerLabel, ok := manifestObj.Labels[labels.ComponentOwner]
	if !ok {
		return nil, fmt.Errorf("label %s not set for manifest resource %s",
			labels.ComponentOwner, namespacedName)
	}

	// extract config
	config := manifestObj.Spec.DefaultConfig

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
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace)
	if err != nil {
		return nil, err
	}

	for _, install := range manifestObj.Spec.Installs {
		chartInfo, err := getChartInfoForInstall(install, codec, manifestObj)
		if err != nil {
			return nil, err
		}

		mergedConfig, mergedChartValues, err := mergeConfigsWithOverrides(ctx, install, defaultClient, manifestObj, configs)
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

func getConfigMapForInstall(ctx context.Context, restClient client.Client, install v1alpha1.InstallInfo,
	manifestObj *v1alpha1.Manifest) (*v1.ConfigMap, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	selector, err := metav1.LabelSelectorAsSelector(&install.OverrideSelector)
	if err != nil {
		return nil, fmt.Errorf("selector invalid: %w", err)
	}
	overrideConfigMaps := &v1.ConfigMapList{}
	if err := restClient.List(ctx, overrideConfigMaps,
		client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("error while fetching ConfigMap for Manifest %s with install %s:  %w",
			namespacedName, install.Name, err)
	}

	if len(overrideConfigMaps.Items) > 1 {
		return nil, fmt.Errorf("selector %s invalid: more than one ConfigMap available "+
			"for Manifest %s with install %s",
			selector.String(), namespacedName, install.Name)
	} else if len(overrideConfigMaps.Items) == 0 {
		return nil, fmt.Errorf("selector %s invalid: no ConfigMap available "+
			"for Manifest %s with install %s",
			selector.String(), namespacedName, install.Name)
	}

	validConfigMap := &overrideConfigMaps.Items[0]
	return validConfigMap, addOwnerRefToConfigMap(ctx, validConfigMap, manifestObj, restClient)
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

func mergeConfigsWithOverrides(ctx context.Context, install v1alpha1.InstallInfo, restClient client.Client,
	manifestObj *v1alpha1.Manifest, configs []interface{}) (map[string]interface{}, map[string]interface{}, error) {

	defaultConfigString, defaultChartValuesString, err := getConfigAndOverridesForInstall(install.Name, configs)
	if err != nil {
		return nil, nil, err
	}

	defaultConfig := map[string]interface{}{}
	if err := strvals.ParseInto(defaultConfigString, defaultConfig); err != nil {
		return nil, nil, err
	}
	defaultChartValues := map[string]interface{}{}
	if err := strvals.ParseInto(defaultChartValuesString, defaultChartValues); err != nil {
		return nil, nil, err
	}

	if install.OverrideSelector.Size() != 0 {
		overridesConfigMap, err := getConfigMapForInstall(ctx, restClient, install, manifestObj)
		if err != nil {
			return nil, nil, err
		}

		var customOverrideConfigs []interface{}
		if err = yaml.Unmarshal([]byte(overridesConfigMap.Data["configs"]), &customOverrideConfigs); err != nil {
			return nil, nil, err
		}

		customConfigString, customChartValuesString, err :=
			getConfigAndOverridesForInstall(install.Name, customOverrideConfigs)
		if err != nil {
			return nil, nil, err
		}

		customConfig := map[string]interface{}{}
		if err := strvals.ParseInto(customConfigString, customConfig); err != nil {
			return nil, nil, err
		}
		customChartValues := map[string]interface{}{}
		if err := strvals.ParseInto(customChartValuesString, customChartValues); err != nil {
			return nil, nil, err
		}

		if err = mergo.Merge(&defaultConfig, customConfig, mergo.WithOverride); err != nil {
			return nil, nil, err
		}
		if err = mergo.Merge(&defaultChartValues, customChartValues, mergo.WithOverride); err != nil {
			return nil, nil, err
		}
	}

	return defaultConfig, defaultChartValues, nil
}
