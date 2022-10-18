package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConsistencyCheckWorkerPool struct {
	Workers
	logger      *logr.Logger
	initialSize int
	size        int
}

func NewConsistencyCheckWorkers(logger *logr.Logger, size int) *ConsistencyCheckWorkerPool {
	return &ConsistencyCheckWorkerPool{
		logger:      logger,
		initialSize: size,
		size:        size,
	}
}

func (mw *ConsistencyCheckWorkerPool) StartWorkers(ctx context.Context, jobChan <-chan ConsistencyCheckRequest,
	handlerFn func(context.Context, *v1alpha1.Manifest, *logr.Logger, client.ObjectKey),
) {
	for worker := 1; worker <= mw.GetWorkerPoolSize(); worker++ {
		go func(ctx context.Context, workerId int, consistencyCheckJob <-chan ConsistencyCheckRequest) {
			mw.logger.Info(fmt.Sprintf("Starting module-manager worker for consistency check with id %d", workerId))
			for {
				select {
				case job := <-consistencyCheckJob:
					mw.logger.Info("Processing consistency check")
					handlerFn(ctx, job.manifestObj, mw.logger, job.namespacedName)
				case <-ctx.Done():
					return
				}
			}
		}(ctx, worker, jobChan)
	}
}

func (mw *ConsistencyCheckWorkerPool) GetWorkerPoolSize() int {
	return mw.size
}

func (mw *ConsistencyCheckWorkerPool) SetWorkerPoolSize(newSize int) {
	if newSize > 0 {
		mw.size = mw.initialSize
	} else {
		mw.size = newSize
	}
}
