/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/gofrs/flock"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
	"helm.sh/helm/v3/pkg/strvals"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"path/filepath"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
	"strings"
	"sync"
	"time"

	"helm.sh/helm/v3/pkg/action"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// ManifestReconciler reconciles a Manifest object
type ManifestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=component.kyma-project.io,resources=manifests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName(req.NamespacedName.String())

	// get manifest object
	manifestObj := v1alpha1.Manifest{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, &manifestObj); err != nil {
		return ctrl.Result{}, err
	}

	// set initial status for processing
	if manifestObj.Status.State == "" {
		manifestObj.Status.State = v1alpha1.ManifestStateProcessing
		if err := r.Status().Update(ctx, &manifestObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	var (
		args = map[string]string{
			// check --set flags parameter from helm
			"set": "",
			// comma seperated values of helm command line flags
			"flags": manifestObj.Spec.ClientConfig,
		}
		repoName    = manifestObj.Spec.RepoName
		url         = manifestObj.Spec.Url
		chartName   = manifestObj.Spec.ChartName
		releaseName = manifestObj.Spec.ReleaseName
	)

	settings := cli.New()

	// evaluate create or delete chart
	create, err := strconv.ParseBool(manifestObj.Spec.CreateChart)
	if err != nil {
		return ctrl.Result{}, err
	}

	if create {
		if err := r.AddHelmRepo(settings, repoName, url, logger); err != nil {
			return ctrl.Result{}, err
		}

		if exists, err := r.GetChart(releaseName, settings, logger); !exists {
			logger.Info(err.Error(), "chart", "not found, installing now..")
			if err := r.InstallChart(settings, logger, releaseName, repoName, chartName, args); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			logger.Info("release already exists for", "chart name", releaseName)
		}

		// update helm chart in a separate go-routine
		if err := r.RepoUpdate(settings, logger); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err := r.UninstallChart(settings, releaseName, logger); err != nil {
			return ctrl.Result{}, err
		}
	}

	// if no errors are reported update status of manifest object to "Ready"
	manifestObj = v1alpha1.Manifest{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, &manifestObj); err != nil {
		return ctrl.Result{}, err
	}

	if manifestObj.Status.State == v1alpha1.ManifestStateProcessing {
		manifestObj.Status.State = v1alpha1.ManifestStateReady
		if err := r.Status().Update(ctx, &manifestObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	// should not be reconciled again
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Manifest{}).
		Complete(r)
}

func (r *ManifestReconciler) GetChart(releaseName string, settings *cli.EnvSettings, logger logr.Logger) (bool, error) {
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		logger.Info("manifest operator debug", format, v)
	}); err != nil {
		return false, err
	}
	actionClient := action.NewGet(actionConfig)
	result, err := actionClient.Run(releaseName)
	if err != nil {
		return false, err
	}
	return result != nil, nil
}

func (r *ManifestReconciler) InstallChart(settings *cli.EnvSettings, logger logr.Logger, releaseName string, repoName string, chartName string, args map[string]string) error {
	// setup helm client
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		logger.Info("manifest operator debug", format, v)
	}); err != nil {
		return err
	}
	actionClient := action.NewInstall(actionConfig)

	// set flags
	clientFlags := map[string]interface{}{}
	if err := strvals.ParseInto(args["flags"], clientFlags); err != nil {
		return err
	}
	clientValue := reflect.Indirect(reflect.ValueOf(actionClient))
	for flagKey, flagValue := range clientFlags {
		value := clientValue.FieldByName(flagKey)
		if !value.IsValid() || !value.CanSet() {
			continue
		}
		switch value.Kind() {
		case reflect.Bool:
			value.SetBool(flagValue.(bool))
		case reflect.Int64:
			value.SetInt(flagValue.(int64))
		case reflect.String:
			value.SetString(flagValue.(string))
		}
	}

	// default versioning if unspecified
	if actionClient.Version == "" && actionClient.Devel {
		actionClient.Version = ">0.0.0-0"
	}

	actionClient.ReleaseName = releaseName

	chartPath, err := actionClient.ChartPathOptions.LocateChart(fmt.Sprintf("%s/%s", repoName, chartName), settings)
	if err != nil {
		return err
	}

	logger.Info("chart located", "path", chartPath)

	allSettings := getter.All(settings)
	valueOpts := &values.Options{}
	mergedValues, err := valueOpts.MergeValues(allSettings)
	if err != nil {
		return err
	}

	// Merge "set" args
	if err := strvals.ParseInto(args["set"], mergedValues); err != nil {
		return err
	}

	// Dependencies check for chart
	chartRequested, err := loader.Load(chartPath)
	if err != nil {
		return err
	}

	if chartRequested.Metadata.Type != "" && chartRequested.Metadata.Type != "application" {
		return fmt.Errorf("%s charts are not installable", chartRequested.Metadata.Type)
	}

	if req := chartRequested.Metadata.Dependencies; req != nil {
		// If CheckDependencies returns an error, we have unfulfilled dependencies.
		// As of Helm 2.4.0, this is treated as a stopping condition:
		// https://github.com/helm/helm/issues/2209
		if err := action.CheckDependencies(chartRequested, req); err != nil {
			if actionClient.DependencyUpdate {
				manager := &downloader.Manager{
					Out:              os.Stdout,
					ChartPath:        chartPath,
					Keyring:          actionClient.ChartPathOptions.Keyring,
					SkipUpdate:       false,
					Getters:          allSettings,
					RepositoryConfig: settings.RepositoryConfig,
					RepositoryCache:  settings.RepositoryCache,
				}
				if err := manager.Update(); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}

	release, err := actionClient.Run(chartRequested, mergedValues)
	if err != nil {
		return err
	}
	logger.Info("release", "manifest", release.Manifest)
	return nil
}

// RepoUpdate
func (r *ManifestReconciler) RepoUpdate(settings *cli.EnvSettings, logger logr.Logger) error {
	repoFile := settings.RepositoryConfig

	f, err := repo.LoadFile(repoFile)
	if os.IsNotExist(errors.Cause(err)) || len(f.Repositories) == 0 {
		return fmt.Errorf("no repositories found. You must add one before updating")
	}
	var repos []*repo.ChartRepository
	for _, cfg := range f.Repositories {
		chartRepo, err := repo.NewChartRepository(cfg, getter.All(settings))
		if err != nil {
			return err
		}
		repos = append(repos, chartRepo)
	}

	logger.Info("Pulling the latest version from your repositories")
	var wg sync.WaitGroup
	for _, re := range repos {
		wg.Add(1)
		go func(re *repo.ChartRepository) {
			defer wg.Done()
			if _, err := re.DownloadIndexFile(); err != nil {
				logger.Error(err, "Unable to get an update from the chart repository", "name", re.Config.Name, "url", re.Config.URL)
			} else {
				logger.Info("Successfully received an update for the chart repository", "name", re.Config.Name)
			}
		}(re)
	}
	wg.Wait()
	logger.Info("Update Complete!! Happy Manifesting!")
	return nil
}

// AddHelmRepo
func (r *ManifestReconciler) AddHelmRepo(settings *cli.EnvSettings, repoName string, url string, logger logr.Logger) error {
	repoFile := settings.RepositoryConfig

	// File locking mechanism
	if err := os.MkdirAll(filepath.Dir(repoFile), os.ModePerm); err != nil && !os.IsExist(err) {
		return err
	}
	fileLock := flock.New(strings.Replace(repoFile, filepath.Ext(repoFile), ".lock", 1))
	lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, time.Second)
	if err == nil && locked {
		defer fileLock.Unlock()
	}

	b, err := ioutil.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var f repo.File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return err
	}

	if f.Has(repoName) {
		logger.Info("helm repo already exists", "name", repoName)
	}

	c := repo.Entry{
		Name: repoName,
		URL:  url,
	}

	chartRepo, err := repo.NewChartRepository(&c, getter.All(settings))
	if err != nil {
		return fmt.Errorf("repository name (%s) already exists\n %w", repoName, err)
	}

	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return fmt.Errorf("looks like %s is not a valid chart repository or cannot be reached %w", url, err)
	}

	f.Update(&c)
	repoConfig := settings.RepositoryConfig
	if err := f.WriteFile(repoConfig, 0644); err != nil {
		return err
	}
	fmt.Printf("%q has been added to your repositories\n", repoName)
	return nil
}

// UninstallChart
func (r *ManifestReconciler) UninstallChart(settings *cli.EnvSettings, releaseName string, logger logr.Logger) error {
	if exists, err := r.GetChart(releaseName, settings, logger); !exists {
		logger.Info(err.Error(), "chart already deleted or does not exist for release", releaseName)
		return nil
	}
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		logger.Info("manifest operator debug", format, v)
	}); err != nil {
		return err
	}
	actionClient := action.NewUninstall(actionConfig)
	response, err := actionClient.Run(releaseName)
	if err != nil {
		return err
	}
	logger.Info("chart deletion executed", "response", response.Release.Info.Description)
	return nil
}
