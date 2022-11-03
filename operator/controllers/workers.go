package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
)

type Workers interface {
	GetWorkerPoolSize() int
	SetWorkerPoolSize(newSize int)
	StartWorkers(ctx context.Context, jobChan <-chan manifestTypes.InstallInfo, handlerFn func(info manifestTypes.InstallInfo,
		logger *logr.Logger) *manifestTypes.InstallResponse)
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

func (mw *ManifestWorkerPool) StartWorkers(ctx context.Context, jobChan <-chan OperationRequest,
	handlerFn func(manifestTypes.InstallInfo, manifestTypes.Mode, *logr.Logger) *manifestTypes.InstallResponse,
) {
	for worker := 1; worker <= mw.GetWorkerPoolSize(); worker++ {
		go func(ctx context.Context, workerId int, deployJob <-chan OperationRequest) {
			mw.logger.Info(fmt.Sprintf("Starting module-manager worker with id %d", workerId))
			for {
				select {
				case deployChart := <-deployJob:
					mw.logger.Info(fmt.Sprintf("Processing chart with name %s by worker with id %d",
						deployChart.Info.ChartName, workerId))
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
