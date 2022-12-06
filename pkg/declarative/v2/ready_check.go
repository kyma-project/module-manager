package v2

import (
	"context"
	"fmt"
	"sync"
	"time"

	mmclient "github.com/kyma-project/module-manager/pkg/client"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"

	"helm.sh/helm/v3/pkg/kube"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

func checkReady(ctx context.Context,
	clients *mmclient.SingletonClients, resources kube.ResourceList,
) (bool, error) {
	start := time.Now()
	logger := log.FromContext(ctx)
	logger.V(util.TraceLogLevel).Info("ReadyCheck", "resources", len(resources))
	clientSet, _ := clients.KubernetesClientSet()
	checker := kube.NewReadyChecker(clientSet, func(format string, args ...interface{}) {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf(format, args...))
	}, kube.PausedAsReady(true), kube.CheckJobs(true))

	readyCheckResults := make(chan error, len(resources))
	readyMu := sync.Mutex{}
	resourcesReady := true

	isReady := func(i int) {
		ready, err := checker.IsReady(ctx, resources[i])
		readyMu.Lock()
		defer readyMu.Unlock()
		if !ready {
			resourcesReady = false
		}
		readyCheckResults <- err
	}

	for i := range resources {
		go isReady(i)
	}

	var errs []error
	for i := 0; i < len(resources); i++ {
		err := <-readyCheckResults
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return resourcesReady, types.NewMultiError(errs)
	}
	logger.V(util.DebugLogLevel).Info("ReadyCheck finished",
		"resources", len(resources), "time", time.Since(start))

	return resourcesReady, nil
}
