package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/operator/pkg/descriptor"
	"path/filepath"
	"time"

	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getReadyConditionForComponent(manifest *v1alpha1.Manifest,
	installName string,
) (*v1alpha1.ManifestCondition, bool) {
	status := &manifest.Status
	for _, existingCondition := range status.Conditions {
		if existingCondition.Type == v1alpha1.ConditionTypeReady && existingCondition.Reason == installName {
			return &existingCondition, true
		}
	}
	return &v1alpha1.ManifestCondition{}, false
}

func addReadyConditionForObjects(manifest *v1alpha1.Manifest, installItems []v1alpha1.InstallItem,
	conditionStatus v1alpha1.ManifestConditionStatus, message string) {
	status := &manifest.Status
	for _, installItem := range installItems {
		condition, exists := getReadyConditionForComponent(manifest, installItem.ChartName)
		if !exists {
			condition = &v1alpha1.ManifestCondition{
				Type:   v1alpha1.ConditionTypeReady,
				Reason: installItem.ChartName,
			}
			status.Conditions = append(status.Conditions, *condition)
		}
		condition.LastTransitionTime = &metav1.Time{Time: time.Now()}
		condition.Message = message
		condition.Status = conditionStatus
		if installItem.ClientConfig != "" || installItem.Overrides != "" {
			condition.InstallInfo = installItem
		}

		for i, existingCondition := range status.Conditions {
			if existingCondition.Type == v1alpha1.ConditionTypeReady &&
				existingCondition.Reason == installItem.ChartName {
				status.Conditions[i] = *condition
				break
			}
		}
	}
}

func prepareDeployInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClient client.Client,
	verifyInstallation bool, customStateCheck bool, codec *v1alpha1.Codec) ([]manifest.DeployInfo, error) {
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

	var chartInfo manifest.ChartInfo

	for _, install := range manifestObj.Spec.Installs {
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

			chartInfo = manifest.ChartInfo{
				ChartName: fmt.Sprintf("%s/%s", install.Name, helmChartSpec.ChartName),
				RepoName:  install.Name,
				Url:       helmChartSpec.Url,
			}

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

			chartInfo = manifest.ChartInfo{
				ChartName: install.Name,
				ChartPath: chartPath,
			}
		}

		// additional configuration check
		var overrides string
		var clientConfig string
		for _, config := range configs {
			mappedConfig, ok := config.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf(configReadError)
			}
			if mappedConfig["name"] == install.Name {
				overrides, ok = mappedConfig["overrides"].(string)
				if !ok {
					return nil, fmt.Errorf(configReadError)
				}
				clientConfig, ok = mappedConfig["clientConfig"].(string)
				if !ok {
					return nil, fmt.Errorf(configReadError)
				}
				break
			}
		}

		// common deploy properties
		chartInfo.ReleaseName = install.Name
		chartInfo.Overrides = overrides
		chartInfo.ClientConfig = clientConfig

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
