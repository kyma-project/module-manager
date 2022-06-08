package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
)

type Workers interface {
	GetWorkerPoolSize() int
	SetWorkerPoolSize(newSize int)
	StartWorkers(jobChan chan v1alpha1.ChartInfo, resultChan chan error, handlerFn func(info v1alpha1.ChartInfo, logger logr.Logger) error)
}

type ManifestWorkers struct {
	Workers
	logger *logr.Logger
	size   int
}

func NewManifestWorkers(logger *logr.Logger) *ManifestWorkers {
	return &ManifestWorkers{
		logger: logger,
		size:   DefaultWorkersCount,
	}
}

func (mw *ManifestWorkers) StartWorkers(ctx context.Context, jobChan chan DeployInfo, handlerFn func(info DeployInfo, logger *logr.Logger) *RequestError) {
	for worker := 1; worker <= mw.GetWorkerPoolSize(); worker++ {
		go func(ctx context.Context, id int, deployJob <-chan DeployInfo) {
			mw.logger.Info(fmt.Sprintf("Starting manifest-operator worker with id:%d", id))
			for {
				select {
				case deployInfo := <-deployJob:
					if deployInfo.ChartInfo != nil {
						deployInfo.RequestErrChan <- handlerFn(deployInfo, mw.logger)
					}
				case <-ctx.Done():
					return
				}
			}
		}(ctx, worker, jobChan)
	}
}

func (mw *ManifestWorkers) GetWorkerPoolSize() int {
	return mw.size
}

func (mw *ManifestWorkers) SetWorkerPoolSize(newSize int) {
	if newSize < 1 {
		mw.size = DefaultWorkersCount
	} else {
		mw.size = newSize
	}
}
