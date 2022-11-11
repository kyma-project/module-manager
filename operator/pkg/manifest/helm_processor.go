package manifest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/diff"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type helm struct {
	clients      *SingletonClients
	settings     *cli.EnvSettings
	actionClient *action.Install
	repoHandler  *RepoHandler
	logger       logr.Logger
	*transformer
	*rendered
}

// verify compliance of interface.
var _ types.RenderSrc = &helm{}

//nolint:gochecknoglobals
var accessor = meta.NewAccessor()

// NewHelmProcessor returns a new instance of the helm processor.
// The returned helm instance contains necessary clients based on rest config and rest mapper,
// combined with helm configuration, like helm native flags and --set flags.
// Additionally, it also transforms the manifest resources based on user defined input.
// On the returned helm instance, installation, uninstallation and verification checks
// can be executed on the resource manifest.
func NewHelmProcessor(clients *SingletonClients, settings *cli.EnvSettings,
	logger logr.Logger, render *rendered, txformer *transformer,
) (types.RenderSrc, error) {
	helmClient := &helm{
		clients:      clients,
		logger:       logger,
		repoHandler:  NewRepoHandler(logger, settings),
		settings:     settings,
		transformer:  txformer,
		rendered:     render,
		actionClient: clients.Install(),
	}

	// verify compliance of interface
	var helmProcessor types.RenderSrc = helmClient

	return helmProcessor, nil
}

// GetRawManifest returns processed resource manifest using helm client.
func (h *helm) GetRawManifest(deployInfo types.InstallInfo) *types.ParsedFile {
	// always override existing flags config
	// to ensure CR updates are reflected on the action client
	err := h.resetFlags(deployInfo)
	if err != nil {
		return types.NewParsedFile("", err)
	}

	chartPath := deployInfo.ChartPath
	if chartPath == "" {
		// legacy case - download chart from helm repo
		chartPath, err = h.downloadChart(deployInfo.RepoName, deployInfo.URL, deployInfo.ChartName)
		if err != nil {
			return types.NewParsedFile("", err)
		}
	}
	h.logger.V(util.DebugLogLevel).Info("chart located", "path", chartPath)

	// if rendered manifest doesn't exist
	// check newly rendered manifest here
	return types.NewParsedFile(h.renderManifestFromChartPath(chartPath, deployInfo.Flags.SetFlags))
}

// Install transforms and applies Helm based manifest using helm client.
func (h *helm) Install(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	// install resources
	result, err := h.installResources(resourceLists)
	if err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("installed | updated Helm chart resources",
		"create count", len(result.Created),
		"update count", len(result.Updated),
		"chart", deployInfo.ChartName,
		"release", deployInfo.ReleaseName,
		"resource", client.ObjectKeyFromObject(deployInfo.BaseResource).String())

	// verify resources
	if ready, err := h.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationCreate); !ready || err != nil {
		return ready, err
	}

	// update helm repositories
	if deployInfo.UpdateRepositories {
		if err = h.updateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

// Uninstall transforms and deletes Helm based manifest using helm client.
func (h *helm) Uninstall(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	// convert for Helm processing
	resourceLists, err := h.parseToResourceLists(stringifedManifest, deployInfo, transforms)
	if err != nil {
		return false, err
	}

	// uninstall resources
	result, err := h.uninstallResources(resourceLists)
	if err != nil {
		return false, err
	}

	h.logger.V(util.DebugLogLevel).Info("uninstalled Helm chart resources",
		"count", len(result.Deleted),
		"chart", deployInfo.ChartName,
		"release", deployInfo.ReleaseName,
		"resource", client.ObjectKeyFromObject(deployInfo.BaseResource).String())

	// verify resource uninstallation
	if ready, err := h.verifyResources(deployInfo.Ctx, resourceLists,
		deployInfo.CheckReadyStates, types.OperationDelete); !ready || err != nil {
		return ready, err
	}

	// update Helm repositories
	if deployInfo.UpdateRepositories {
		if err = h.updateRepos(deployInfo.Ctx); err != nil {
			return false, err
		}
	}

	return true, nil
}

// IsConsistent indicates if helm installation is consistent with the desired manifest resources.
func (h *helm) IsConsistent(stringifedManifest string, deployInfo types.InstallInfo, transforms []types.ObjectTransform,
) (bool, error) {
	desired, err := h.Transform(deployInfo.Ctx, stringifedManifest, deployInfo.BaseResource, transforms)
	if err != nil {
		return false, err
	}
	desiredResources := kube.ResourceList{}
	for _, unstructuredObject := range desired.Items {
		resourceInfo, err := h.clients.ResourceInfo(unstructuredObject)
		if err != nil {
			return false, err
		}
		desiredResources = append(desiredResources, resourceInfo)
	}
	live := make([]*unstructured.Unstructured, 0, len(desiredResources))
	err = desiredResources.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		helper := resource.NewHelper(info.Client, info.Mapping)
		_, err = helper.Get(info.Namespace, info.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				live = append(live, nil)
				return nil
			}
			return errors.Wrapf(err, "could not get information about the resource %s / %s", info.Name, info.Namespace)
		}

		unstructFromInfoObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&info.Object)
		if err != nil {
			return err
		}
		live = append(live, &unstructured.Unstructured{Object: unstructFromInfoObj})
		return nil
	})
	if err != nil {
		return false, err
	}

	resList, err := diff.Array(desired.Items, live)

	h.logger.V(util.DebugLogLevel).Info("diffing completed",
		"modified", resList.Modified)
	if err != nil {
		return false, err
	}

	return !resList.Modified, nil
}

func (h *helm) verifyResources(ctx context.Context, resourceLists types.ResourceLists, verifyReadyStates bool,
	operationType types.HelmOperation,
) (bool, error) {
	return h.checkWaitForResources(ctx, resourceLists.GetWaitForResources(), operationType,
		verifyReadyStates)
}

func (h *helm) installResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	// create namespace resource first!
	if len(resourceLists.Namespace) > 0 {
		if _, err := h.clients.KubeClient().Create(resourceLists.Namespace); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
	}

	// fresh install
	if resourceLists.Installed == nil && len(resourceLists.Target) > 0 {
		return h.clients.KubeClient().Create(resourceLists.Target)
	}

	// missing resources - update with 3 way merge
	return h.clients.KubeClient().Update(resourceLists.Installed, resourceLists.Target, false)
}

func (h *helm) uninstallResources(resourceLists types.ResourceLists) (*kube.Result, error) {
	var response *kube.Result
	var delErrors []error
	if resourceLists.Installed != nil {
		// add namespace to deleted resources
		response, delErrors = h.clients.KubeClient().Delete(resourceLists.GetResourcesToBeDeleted())
		if len(delErrors) > 0 {
			var wrappedError error
			for _, err := range delErrors {
				wrappedError = fmt.Errorf("%w", err)
			}
			return nil, wrappedError
		}
	}
	return response, nil
}

func (h *helm) checkWaitForResources(ctx context.Context, targetResources kube.ResourceList,
	operation types.HelmOperation, verifyWithoutTimeout bool,
) (bool, error) {
	// verifyWithoutTimeout flag checks native resources are in their respective ready states
	// without a timeout defined
	if verifyWithoutTimeout {
		if operation == types.OperationDelete {
			return checkResourcesDeleted(targetResources)
		}

		readyChecker := h.clients.ReadyChecker(func(format string, v ...interface{}) {},
			kube.PausedAsReady(true), kube.CheckJobs(true))
		return checkReady(ctx, targetResources, readyChecker)
	}

	// if Wait or WaitForJobs is enabled, resources are verified to be in ready state with a timeout
	if !h.clients.Install().Wait || h.clients.Install().Timeout == 0 {
		// return here as ready, since waiting flags were not set
		return true, nil
	}

	if operation == types.OperationDelete {
		// WaitForDelete reports an error if resources are not deleted in the specified timeout
		return true, h.clients.KubeClient().WaitForDelete(targetResources, h.clients.Install().Timeout)
	}

	if h.clients.Install().WaitForJobs {
		// WaitWithJobs reports an error if resources are not deleted in the specified timeout
		return true, h.clients.KubeClient().WaitWithJobs(targetResources, h.clients.Install().Timeout)
	}
	// Wait reports an error if resources are not deleted in the specified timeout
	return true, h.clients.KubeClient().Wait(targetResources, h.clients.Install().Timeout)
}

func (h *helm) updateRepos(ctx context.Context) error {
	return h.repoHandler.Update(ctx)
}

func (h *helm) resetFlags(deployInfo types.InstallInfo) error {
	// set preliminary flag defaults
	h.setDefaultFlags(deployInfo.ReleaseName)

	// set user defined flags
	return h.setCustomFlags(deployInfo.Flags)
}

func (h *helm) downloadChart(repoName, url, chartName string) (string, error) {
	err := h.repoHandler.Add(repoName, url)
	if err != nil {
		return "", err
	}
	return h.clients.Install().ChartPathOptions.LocateChart(chartName, h.settings)
}

func (h *helm) renderManifestFromChartPath(chartPath string, flags types.Flags) (string, error) {
	// if rendered manifest doesn't exist
	chartRequested, err := h.repoHandler.LoadChart(chartPath, h.clients.Install())
	if err != nil {
		return "", err
	}

	// retrieve manifest
	release, err := h.clients.Install().Run(chartRequested, flags)
	if err != nil {
		return "", err
	}

	return release.Manifest, nil
}

func (h *helm) setDefaultFlags(releaseName string) {
	h.clients.Install().DryRun = true
	h.clients.Install().Atomic = false

	h.clients.Install().WaitForJobs = false

	h.clients.Install().Replace = true     // Skip the name check
	h.clients.Install().IncludeCRDs = true // include CRDs in the templated output
	h.clients.Install().UseReleaseName = false
	h.clients.Install().ReleaseName = releaseName

	// ClientOnly has no interaction with the API server
	// So unless mentioned no additional API Versions can be used as part of helm chart installation
	h.clients.Install().ClientOnly = false

	h.clients.Install().Namespace = v1.NamespaceDefault
	// this will prohibit resource conflict validation while uninstalling
	h.clients.Install().IsUpgrade = true

	// default versioning if unspecified
	if h.clients.Install().Version == "" && h.clients.Install().Devel {
		h.clients.Install().Version = ">0.0.0-0"
	}
}

func (h *helm) setCustomFlags(flags types.ChartFlags) error {
	clientValue := reflect.Indirect(reflect.ValueOf(h.clients.Install()))

	// TODO: as per requirements add more Kind types
	for flagKey, flagValue := range flags.ConfigFlags {
		value := clientValue.FieldByName(flagKey)
		if !value.IsValid() || !value.CanSet() {
			continue
		}

		validConversion := true

		//nolint:exhaustive
		switch value.Kind() {
		case reflect.Bool:
			var valueToBeSet bool
			valueToBeSet, validConversion = flagValue.(bool)
			if validConversion {
				value.SetBool(valueToBeSet)
			}
		case reflect.Int, reflect.Int64:
			var valueToBeSet int64
			valueToBeSet, validConversion = flagValue.(int64)
			if validConversion {
				value.SetInt(valueToBeSet)
			} else {
				var fallbackInt64 time.Duration
				fallbackInt64, validConversion = flagValue.(time.Duration)
				if validConversion {
					value.SetInt(int64(fallbackInt64))
				}
			}
		case reflect.String:
			var valueToBeSet string
			valueToBeSet, validConversion = flagValue.(string)
			if validConversion {
				value.SetString(valueToBeSet)
			}
		}

		if !validConversion {
			return fmt.Errorf("unsupported flag value %s:%v", flagKey, flagValue)
		}
	}
	return nil
}

func (h *helm) parseToResourceLists(stringifiedManifest string, deployInfo types.InstallInfo,
	transforms []types.ObjectTransform,
) (types.ResourceLists, error) {
	nsResourceList, err := h.GetNsResource()
	if err != nil {
		return types.ResourceLists{}, err
	}

	targetResourceList, err := h.getTargetResources(deployInfo.Ctx, stringifiedManifest,
		transforms, deployInfo.BaseResource)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render resources from manifest: %w", err)
	}

	existingResourceList, err := util.FilterExistingResources(targetResourceList)
	if err != nil {
		return types.ResourceLists{}, fmt.Errorf("could not render existing resources from manifest: %w", err)
	}

	return types.ResourceLists{
		Target:    targetResourceList,
		Installed: existingResourceList,
		Namespace: nsResourceList,
	}, nil
}

func (h *helm) GetNsResource() (kube.ResourceList, error) {
	// set kubeclient namespace for override
	// TODO remove setting in kubeclient
	h.clients.KubeClient().Namespace = h.clients.Install().Namespace

	// validate namespace parameters
	// proceed only if not default namespace since it already exists
	if !h.clients.Install().CreateNamespace || h.clients.Install().Namespace == v1.NamespaceDefault {
		return nil, nil
	}

	ns := h.clients.Install().Namespace
	nsBuf, err := util.GetNamespaceObjBytes(ns)
	if err != nil {
		return nil, err
	}
	return h.clients.KubeClient().Build(bytes.NewBuffer(nsBuf), false)
}

func (h *helm) getTargetResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	var err error

	if len(transforms) == 0 {
		resourceList, err = h.clients.KubeClient().Build(bytes.NewBufferString(manifest), false)
	} else {
		resourceList, err = h.transformManifestResources(ctx, manifest, transforms, object)
	}

	if err != nil {
		return nil, err
	}

	// verify namespace override if not done by kubeclient
	if err = overrideNamespace(resourceList, h.clients.Install().Namespace); err != nil {
		return nil, err
	}
	return resourceList, nil
}

func (h *helm) transformManifestResources(ctx context.Context, manifest string,
	transforms []types.ObjectTransform, object types.BaseCustomObject,
) (kube.ResourceList, error) {
	var resourceList kube.ResourceList
	objects, err := h.Transform(ctx, manifest, object, transforms)
	if err != nil {
		return nil, err
	}

	for _, unstructuredObject := range objects.Items {
		resourceInfo, err := h.clients.ResourceInfo(unstructuredObject)
		if err != nil {
			return nil, err
		}
		resourceList = append(resourceList, resourceInfo)
	}
	return resourceList, err
}
