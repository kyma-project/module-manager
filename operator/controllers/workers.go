package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/operator/lib/manifest"
)

type Workers interface {
	GetWorkerPoolSize() int
	SetWorkerPoolSize(newSize int)
	StartWorkers(ctx context.Context, jobChan <-chan manifest.DeployInfo, handlerFn func(info manifest.DeployInfo,
		logger *logr.Logger) *manifest.ChartResponse)
}

type ManifestWorkerPool struct {
	Workers
	logger      *logr.Logger
	initialSize int
	size        int
}

func NewManifestWorkers(logger *logr.Logger, workersConcurrentManifests int) *ManifestWorkerPool {
	return &ManifestWorkerPool{
		logger:      logger,
		initialSize: workersConcurrentManifests,
		size:        workersConcurrentManifests,
	}
}

func (mw *ManifestWorkerPool) StartWorkers(ctx context.Context, jobChan <-chan ManifestDeploy,
	handlerFn func(info manifest.DeployInfo, mode manifest.Mode, logger *logr.Logger) *manifest.ChartResponse) {
	for worker := 1; worker <= mw.GetWorkerPoolSize(); worker++ {
		go func(ctx context.Context, id int, deployJob <-chan ManifestDeploy) {
			mw.logger.Info(fmt.Sprintf("Starting manifest-operator worker with id %d", id))
			for {
				select {
				case deployChart := <-deployJob:
					mw.logger.Info(fmt.Sprintf("Processing chart with name %s by worker with id %d",
						deployChart.Info.ChartName, id))
					deployChart.ResponseChan <- handlerFn(deployChart.Info, deployChart.Mode, mw.logger)
				case <-ctx.Done():
					return
				}
			}
		}(ctx, worker, jobChan)
	}
}

func (mw *ManifestWorkerPool) GetWorkerPoolSize() int {
	return mw.size
}

func (mw *ManifestWorkerPool) SetWorkerPoolSize(newSize int) {
	if newSize > 0 {
		mw.size = mw.initialSize
	} else {
		mw.size = newSize
	}
}
