package prepare

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/operator/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/lib/crd"
	"github.com/kyma-project/manifest-operator/operator/lib/custom"
	"github.com/kyma-project/manifest-operator/operator/lib/descriptor"
	"github.com/kyma-project/manifest-operator/operator/lib/labels"
	"github.com/kyma-project/manifest-operator/operator/lib/manifest"
	"github.com/kyma-project/manifest-operator/operator/lib/types"
	manifestCustom "github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"helm.sh/helm/v3/pkg/strvals"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	configReadError = "reading install %s resulted in an error for " + v1alpha1.ManifestKind + " %s"
	configFileName  = "installConfig.yaml"
)

func GetDeployInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClient client.Client,
	verifyInstallation bool, customStateCheck bool, codec *types.Codec, defaultRestConfig *rest.Config,
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
		filepath.Join(fmt.Sprintf("%s", config.Ref), configFileName))
	if err != nil {
		return nil, err
	}
	installConfigObj, ok := decodedConfig.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(configReadError, ".spec.config", namespacedName)
	}
	configs, ok := installConfigObj["configs"].([]interface{})
	if !ok {
		return nil, fmt.Errorf(configReadError, "chart config object of .spec.config", namespacedName)
	}

	// evaluate rest config
	customResCheck := &manifestCustom.CustomResourceCheck{DefaultClient: defaultClient}

	// evaluate rest config
	clusterClient := &custom.ClusterClient{DefaultClient: defaultClient}
	restConfig, err := clusterClient.GetRestConfig(ctx, kymaOwnerLabel, manifestObj.Namespace, defaultRestConfig)
	if err != nil {
		return nil, err
	}

	destinationClient, err := clusterClient.GetNewClient(restConfig, client.Options{})
	if err != nil {
		return nil, err
	}

	// check crds - if present do not update
	if err = installCrds(ctx, destinationClient, manifestObj.Spec.CRDs); err != nil {
		return nil, err
	}

	// check cr - if present do to not update
	if err = installCr(ctx, destinationClient, manifestObj.Spec.Resource); err != nil {
		return nil, err
	}

	// installs
	for _, install := range manifestObj.Spec.Installs {
		chartInfo, err := getChartInfoForInstall(install, codec, manifestObj)
		if err != nil {
			return nil, err
		}

		chartConfig, chartValues, err := parseChartConfigAndValues(install, configs, namespacedName.String())
		if err != nil {
			return nil, err
		}

		// common deploy properties
		chartInfo.ReleaseName = install.Name
		chartInfo.Overrides = chartValues
		chartInfo.ClientConfig = chartConfig

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

func installCrds(ctx context.Context, destinationClient client.Client, crdImage types.ImageSpec) error {
	// extract helm chart from layer digest
	crdsPath, err := descriptor.GetPathFromExtractedTarGz(crdImage.Repo, crdImage.Name, crdImage.Ref,
		fmt.Sprintf("%s-%s", crdImage.Name, crdImage.Ref))
	if err != nil {
		return err
	}

	crds, err := crd.GetCRDsFromPath(ctx, crdsPath)
	if err != nil {
		return err
	}

	if err = crd.CreateCRDs(ctx, crds, destinationClient); err != nil {
		return err
	}

	return nil
}

func installCr(ctx context.Context, destinationClient client.Client, resource unstructured.Unstructured) error {
	// check cr
	existingCustomResource := unstructured.Unstructured{}
	existingCustomResource.SetGroupVersionKind(resource.GroupVersionKind())
	customResourceKey := client.ObjectKey{
		Name:      resource.GetName(),
		Namespace: resource.GetNamespace(),
	}
	err := destinationClient.Get(ctx, customResourceKey, &existingCustomResource)
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	if err != nil {
		if err = destinationClient.Create(ctx, &resource); err != nil {
			return err
		}
	}

	return nil
}

func getChartInfoForInstall(install v1alpha1.InstallInfo, codec *types.Codec,
	manifestObj *v1alpha1.Manifest) (*manifest.ChartInfo, error) {
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
			Url:       helmChartSpec.Url,
		}, nil

	case types.OciRefType:
		var imageSpec types.ImageSpec
		if err = codec.Decode(install.Source.Raw, &imageSpec, specType); err != nil {
			return nil, err
		}

		// extract helm chart from layer digest
		chartPath, err := descriptor.GetPathFromExtractedTarGz(imageSpec.Repo, imageSpec.Name, imageSpec.Ref,
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

func getConfigAndValuesForInstall(installName string, configs []interface{}, namespacedName string) (
	string, string, error) {
	var defaultOverrides string
	var clientConfig string

	for _, config := range configs {
		mappedConfig, ok := config.(map[string]interface{})
		if !ok {
			return "", "", fmt.Errorf(configReadError, "config object", namespacedName)
		}
		if mappedConfig["name"] == installName {
			defaultOverrides, ok = mappedConfig["overrides"].(string)
			if !ok {
				return "", "", fmt.Errorf(configReadError, "config object overrides", namespacedName)
			}
			clientConfig, ok = mappedConfig["clientConfig"].(string)
			if !ok {
				return "", "", fmt.Errorf(configReadError, "chart config", namespacedName)
			}
			break
		}
	}
	return clientConfig, defaultOverrides, nil
}

func parseChartConfigAndValues(install v1alpha1.InstallInfo, configs []interface{},
	namespacedName string) (
	map[string]interface{}, map[string]interface{}, error) {

	configString, valuesString, err := getConfigAndValuesForInstall(install.Name, configs, namespacedName)
	if err != nil {
		return nil, nil, err
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
