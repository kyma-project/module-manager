package prepare

import (
	"context"
	"errors"
	"fmt"
	"io"

	"helm.sh/helm/v3/pkg/strvals"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	manifestCustom "github.com/kyma-project/module-manager/operator/internal/pkg/custom"
	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"github.com/kyma-project/module-manager/operator/pkg/descriptor"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/resource"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

const configReadError = "reading install %s resulted in an error for " + v1alpha1.ManifestKind

// GetInstallInfos pre-processes the passed Manifest CR and returns a list types.InstallInfo objects,
// each representing an installation artifact.
func GetInstallInfos(ctx context.Context, manifestObj *v1alpha1.Manifest, defaultClusterInfo types.ClusterInfo,
	flags internalTypes.ReconcileFlagConfig, processorCache types.RendererCache,
) ([]types.InstallInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)

	// extract config
	config := manifestObj.Spec.Config

	var configs []any
	if config.Type.NotEmpty() { //nolint:nestif
		decodedConfig, err := descriptor.DecodeYamlFromDigest(config)
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
	clusterInfo, err := getDestinationConfigAndClient(ctx, defaultClusterInfo, manifestObj, processorCache,
		flags.CustomRESTCfg)
	if err != nil {
		return nil, err
	}

	// ensure runtime-watcher labels are set to CustomResource
	InsertWatcherLabels(manifestObj)

	// parse installs
	baseDeployInfo := types.InstallInfo{
		ClusterInfo: clusterInfo,
		ResourceInfo: types.ResourceInfo{
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

func getDestinationConfigAndClient(ctx context.Context, defaultClusterInfo types.ClusterInfo,
	manifestObj *v1alpha1.Manifest, processorCache types.RendererCache, customCfgGetter internalTypes.RESTConfigGetter,
) (types.ClusterInfo, error) {
	// in single cluster mode return the default cluster info
	// since the resources need to be installed in the same cluster
	if !manifestObj.Spec.Remote {
		return defaultClusterInfo, nil
	}

	kymaOwnerLabel, err := util.GetResourceLabel(manifestObj, labels.ComponentOwner)
	if err != nil {
		return types.ClusterInfo{}, err
	}

	// cluster info record from cluster cache
	kymaNsName := client.ObjectKey{Name: kymaOwnerLabel, Namespace: manifestObj.Namespace}
	processor := processorCache.GetProcessor(kymaNsName)
	if processor != nil {
		return processor.GetClusterInfo()
	}

	// RESTConfig can either be retrieved by a secret with name contained in labels.ComponentOwner Manifest CR label,
	// or it can be retrieved as a function return value, passed during controller startup.
	var restConfigGetter internalTypes.RESTConfigGetter
	if customCfgGetter != nil {
		restConfigGetter = customCfgGetter
	} else {
		restConfigGetter = getDefaultRESTConfigGetter(ctx, kymaOwnerLabel, manifestObj.Namespace,
			defaultClusterInfo.Client)
	}
	restConfig, err := restConfigGetter()
	if err != nil {
		return types.ClusterInfo{}, err
	}

	return types.ClusterInfo{
		Config: restConfig,
		// client will be set during processing of manifest
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
	configs []interface{}, baseDeployInfo types.InstallInfo, insecureRegistry bool,
) ([]types.InstallInfo, error) {
	namespacedName := client.ObjectKeyFromObject(manifestObj)
	deployInfos := make([]types.InstallInfo, 0)

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
		chartInfo.Flags = types.ChartFlags{
			ConfigFlags: chartConfig,
			SetFlags:    chartValues,
		}

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
) (*types.ChartInfo, error) {
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

		return &types.ChartInfo{
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

		return &types.ChartInfo{
			ChartName: install.Name,
			ChartPath: chartPath,
		}, nil
	case types.KustomizeType:
		var kustomizeSpec types.KustomizeSpec
		if err = codec.Decode(install.Source.Raw, &kustomizeSpec, specType); err != nil {
			return nil, err
		}

		return &types.ChartInfo{
			ChartName: install.Name,
			ChartPath: kustomizeSpec.Path,
			URL:       kustomizeSpec.URL,
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
	// Make sure Manifest CR is enabled for remote and Spec.Resource is a valid resource
	if !manifestObj.Spec.Remote || manifestObj.Spec.Resource.GetKind() == "" {
		return
	}

	manifestLabels := manifestObj.Spec.Resource.GetLabels()

	ownedByValue := fmt.Sprintf(labels.OwnedByFormat, manifestObj.Namespace, manifestObj.Name)

	if manifestLabels == nil {
		manifestLabels = make(map[string]string)
	}

	manifestLabels[labels.OwnedByLabel] = ownedByValue
	manifestLabels[labels.WatchedByLabel] = labels.OperatorName

	manifestObj.Spec.Resource.SetLabels(manifestLabels)
}

func getDefaultRESTConfigGetter(ctx context.Context, secretName string, namespace string,
	client client.Client,
) internalTypes.RESTConfigGetter {
	return func() (*rest.Config, error) {
		// evaluate remote rest config from secret
		clusterClient := &custom.ClusterClient{DefaultClient: client}
		return clusterClient.GetRESTConfig(ctx, secretName, namespace)
	}
}
