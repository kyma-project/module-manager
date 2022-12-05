package v2

import (
	"context"
	"fmt"
	"sync"
	"time"

	moduleClient "github.com/kyma-project/module-manager/pkg/client"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"helm.sh/helm/v3/pkg/kube"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/cli-runtime/pkg/resource"
)

func replaceSyncedWithResources(status Status, resources []*resource.Info) Status {
	status.Synced = make([]Resource, 0, len(resources))
	for _, info := range resources {
		status.Synced = append(
			status.Synced, Resource{
				Name:      info.Name,
				Namespace: info.Namespace,
				GroupVersionKind: metav1.GroupVersionKind{
					Group:   info.Mapping.GroupVersionKind.Group,
					Version: info.Mapping.GroupVersionKind.Version,
					Kind:    info.Mapping.GroupVersionKind.Kind,
				},
			},
		)
	}
	return status
}

func resourcesFromStatus(clients *moduleClient.SingletonClients, status Status) (kube.ResourceList, error) {
	var current []*resource.Info
	errs := make([]error, 0, len(status.Synced))
	for _, synced := range status.Synced {
		obj := &unstructured.Unstructured{}
		obj.SetName(synced.Name)
		obj.SetNamespace(synced.Namespace)
		obj.SetGroupVersionKind(
			schema.GroupVersionKind{
				Group:   synced.Group,
				Version: synced.Version,
				Kind:    synced.Kind,
			},
		)
		resourceInfo, err := clients.ResourceInfo(obj, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		current = append(current, resourceInfo)
	}

	if len(errs) > 0 {
		return current, types.NewMultiError(errs)
	}
	normaliseNamespaces(clients, current)
	return current, nil
}

func resourcesFromManifest(clients *moduleClient.SingletonClients, resources *types.ManifestResources) (kube.ResourceList, error) {
	var target kube.ResourceList
	errs := make([]error, 0, len(resources.Items))
	for _, obj := range resources.Items {
		resourceInfo, err := clients.ResourceInfo(obj, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		target = append(target, resourceInfo)
	}
	if len(errs) > 0 {
		return nil, types.NewMultiError(errs)
	}
	normaliseNamespaces(clients, target)
	return target, nil
}

// normaliseNamespaces is only a workaround for malformed resources, e.g. by bad charts or wrong type configs.
func normaliseNamespaces(
	clients *moduleClient.SingletonClients, infos []*resource.Info,
) {
	for _, info := range infos {
		obj, ok := info.Object.(metav1.Object)
		if !ok {
			continue
		}
		if info.Namespaced() {
			if info.Namespace == "" || obj.GetNamespace() == "" {
				defaultNamespace := clients.KubeClient().Namespace
				info.Namespace = defaultNamespace
				obj.SetNamespace(defaultNamespace)
			}
		} else {
			if info.Namespace != "" || obj.GetNamespace() != "" {
				info.Namespace = ""
				obj.SetNamespace("")
			}
		}
	}
}

func resourcesServerSideApply(
	ctx context.Context,
	clients *moduleClient.SingletonClients,
	owner client.FieldOwner,
	resources []*resource.Info,
) error {
	ssaStart := time.Now()
	logger := log.FromContext(ctx, "owner", owner)
	logger.V(util.DebugLogLevel).Info("ServerSideApply", "resources", len(resources))

	// Runtime Complexity of this Branch is N as only ServerSideApplier Patch is required
	patchResults := make(chan error, len(resources))

	patch := func(ctx context.Context, info *resource.Info) {
		patchStart := time.Now()

		obj, isTyped := info.Object.(client.Object)
		if !isTyped {
			patchResults <- fmt.Errorf("client object conversion in SSA for %s failed", info.ObjectName())
			return
		}

		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("updating %s", info.ObjectName()),
			"time", time.Since(patchStart))

		if err := clients.Client.Patch(ctx, obj,
			client.Apply, client.ForceOwnership, owner,
		); err != nil {
			patchResults <- fmt.Errorf("SSA for %s failed: %w", info.ObjectName(), err)
		} else {
			patchResults <- nil
		}
	}

	for i := range resources {
		i := i
		go patch(ctx, resources[i])
	}

	var errs []error
	for i := 0; i < len(resources); i++ {
		if err := <-patchResults; err != nil {
			errs = append(errs, err)
		}
	}

	ssaFinish := time.Since(ssaStart)

	if errs != nil {
		return fmt.Errorf("ServerSideApply failed (after %s): %w", ssaFinish, types.NewMultiError(errs))
	}
	logger.V(util.DebugLogLevel).Info("ServerSideApply finished", "time", ssaFinish)

	return nil
}

func deleteAndVerify(clients *moduleClient.SingletonClients, resources kube.ResourceList) (bool, error) {
	if len(resources) > 0 {
		_, errs := clients.KubeClient().Delete(resources)
		if errs != nil {
			return false, types.NewMultiError(errs)
		}
	}
	for i := range resources {
		err := resources[i].Get()
		if err == nil {
			return false, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return true, nil
}

func checkReady(ctx context.Context,
	clients *moduleClient.SingletonClients, resources kube.ResourceList,
) (bool, error) {
	start := time.Now()
	logger := log.FromContext(ctx)
	logger.V(util.DebugLogLevel).Info("ReadyCheck", "resources", len(resources))
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
	logger.V(util.DebugLogLevel).Info("ReadyCheck finished", "time", time.Since(start))

	return resourcesReady, nil
}
