package manifest

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	"sigs.k8s.io/yaml"
)

type RepoHandler struct {
	settings *cli.EnvSettings
	logger   *logr.Logger
}

func NewRepoHandler(logger *logr.Logger, settings *cli.EnvSettings) *RepoHandler {
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

func (r *RepoHandler) Update() error {
	repoFile := r.settings.RepositoryConfig

	f, err := repo.LoadFile(repoFile)
	if os.IsNotExist(errors.Cause(err)) || len(f.Repositories) == 0 {
		return fmt.Errorf("no repositories found. You must add one before updating")
	}
	var repos []*repo.ChartRepository
	for _, cfg := range f.Repositories {
		chartRepo, err := repo.NewChartRepository(cfg, getter.All(r.settings))
		if err != nil {
			return err
		}
		repos = append(repos, chartRepo)
	}

	r.logger.Info("Pulling the latest version from your repositories")
	var waitGroup sync.WaitGroup
	for _, re := range repos {
		waitGroup.Add(1)
		go func(re *repo.ChartRepository) {
			defer waitGroup.Done()
			if _, err := re.DownloadIndexFile(); err != nil {
				r.logger.Error(err, "Unable to get an update from the chart repository", "name", re.Config.Name, "url", re.Config.URL)
			} else {
				r.logger.Info("Successfully received an update for the chart repository", "name", re.Config.Name)
			}
		}(re)
	}
	waitGroup.Wait()
	r.logger.Info("Update Complete!! Happy Manifesting!")
	return nil
}

func (r *RepoHandler) Add(repoName string, url string) error {
	repoFile := r.settings.RepositoryConfig

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

	var file repo.File
	if err := yaml.Unmarshal(b, &file); err != nil {
		return err
	}

	if file.Has(repoName) {
		r.logger.Info("manifest repo already exists", "name", repoName)
	}

	c := repo.Entry{
		Name: repoName,
		URL:  url,
	}

	chartRepo, err := repo.NewChartRepository(&c, getter.All(r.settings))
	if err != nil {
		return fmt.Errorf("repository name (%s) already exists\n %w", repoName, err)
	}

	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return fmt.Errorf("looks like %s is not a valid chart repository or cannot be reached %w", url, err)
	}

	file.Update(&c)
	repoConfig := r.settings.RepositoryConfig
	if err := file.WriteFile(repoConfig, 0o644); err != nil {
		return err
	}
	fmt.Printf("%q has been added to your repositories\n", repoName)
	return nil
}
