package manifest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kyma-project/module-manager/operator/pkg/util"

	"github.com/go-logr/logr"
	"github.com/gofrs/flock"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const (
	lockTimeout             = 30 * time.Second
	ownerWriteUniversalRead = 0o644
)

type RepoHandler struct {
	settings *cli.EnvSettings
	logger   logr.Logger
}

func NewRepoHandler(logger logr.Logger, settings *cli.EnvSettings) *RepoHandler {
	return &RepoHandler{
		settings: settings,
		logger:   logger,
	}
}

func (r *RepoHandler) LoadChart(chartPath string, actionClient *action.Install) (*chart.Chart, error) {
	chartRequested, err := loader.Load(chartPath)
	if err != nil {
		return nil, err
	}

	if chartRequested.Metadata.Type != "" && chartRequested.Metadata.Type != "application" {
		return nil, fmt.Errorf("%s charts are not installable", chartRequested.Metadata.Type)
	}

	//nolint:nestif
	if req := chartRequested.Metadata.Dependencies; req != nil {
		// If CheckDependencies returns an error, we have unfulfilled dependencies.
		// As of Helm 2.4.0, this is treated as a stopping condition:
		// https://github.com/helm/helm/issues/2209
		if err = action.CheckDependencies(chartRequested, req); err != nil {
			if actionClient.DependencyUpdate {
				manager := &downloader.Manager{
					Out:              os.Stdout,
					ChartPath:        chartPath,
					Keyring:          actionClient.ChartPathOptions.Keyring,
					SkipUpdate:       false,
					Getters:          getter.All(r.settings),
					RepositoryConfig: r.settings.RepositoryConfig,
					RepositoryCache:  r.settings.RepositoryCache,
				}
				if err = manager.Update(); err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
	}

	return chartRequested, nil
}

func (r *RepoHandler) Update(ctx context.Context) error {
	logger := log.FromContext(ctx)
	repos := make([]*repo.ChartRepository, 0)
	repoFile := r.settings.RepositoryConfig

	file, err := repo.LoadFile(repoFile)

	// if helm was never used to deploy remote helm charts, repo file would not exist
	if os.IsNotExist(errors.Cause(err)) {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("repository file %s doesn't exist", repoFile))
		return nil
	}

	// for local helm charts there will be no repositories
	if len(file.Repositories) == 0 {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("no repositories found in repository file %s", repoFile))
		return nil
	}

	for _, cfg := range file.Repositories {
		chartRepo, err := repo.NewChartRepository(cfg, getter.All(r.settings))
		if err != nil {
			return err
		}
		repos = append(repos, chartRepo)
	}

	r.logger.V(util.DebugLogLevel).Info("Pulling the latest version from your repositories")
	r.updateRepos(repos)
	r.logger.V(util.DebugLogLevel).Info("Update Complete!! Happy Manifesting!")
	return nil
}

func (r *RepoHandler) updateRepos(repos []*repo.ChartRepository) {
	var waitGroup sync.WaitGroup
	for _, repository := range repos {
		waitGroup.Add(1)
		go func(repository *repo.ChartRepository) {
			defer waitGroup.Done()
			if _, err := repository.DownloadIndexFile(); err != nil {
				r.logger.V(util.DebugLogLevel).Error(err, "Unable to get an update from the chart repository", "name",
					repository.Config.Name, "url", repository.Config.URL)
			} else {
				r.logger.V(util.DebugLogLevel).Info("Successfully received an update for the chart repository", "name",
					repository.Config.Name)
			}
		}(repository)
	}
	waitGroup.Wait()
}

func (r *RepoHandler) Add(repoName string, url string) error {
	repoFile := r.settings.RepositoryConfig

	// File locking mechanism
	if err := os.MkdirAll(filepath.Dir(repoFile), os.ModePerm); err != nil && !os.IsExist(err) {
		return err
	}
	fileLock := flock.New(strings.Replace(repoFile, filepath.Ext(repoFile), ".lock", 1))
	lockCtx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, time.Second)
	if err == nil && locked {
		//nolint:errcheck
		defer fileLock.Unlock()
	}

	fileBytes, err := os.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var file repo.File
	if err := yaml.Unmarshal(fileBytes, &file); err != nil {
		return err
	}

	if file.Has(repoName) {
		r.logger.V(util.DebugLogLevel).Info("manifest repo already exists", "name", repoName)
	}

	repoEntry := repo.Entry{
		Name: repoName,
		URL:  url,
	}

	chartRepo, err := repo.NewChartRepository(&repoEntry, getter.All(r.settings))
	if err != nil {
		return fmt.Errorf("repository name (%s) already exists\n %w", repoName, err)
	}

	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return fmt.Errorf("looks like %s is not a valid chart repository or cannot be reached %w", url, err)
	}

	file.Update(&repoEntry)
	repoConfig := r.settings.RepositoryConfig
	if err := file.WriteFile(repoConfig, ownerWriteUniversalRead); err != nil {
		return err
	}
	r.logger.V(util.DebugLogLevel).Info(fmt.Sprintf("%s has been added to your repositories\n", repoName))
	return nil
}
