package v2

import (
	"context"
	"fmt"
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

func replaceSyncedWithResources(status Status, resources []*resource.Info) {
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
}

func resourcesFromStatus(clients *moduleClient.SingletonClients, status Status) (kube.ResourceList, error) {
	var current []*resource.Info
	errs := make([]error, 0, len(status.Synced))
	for _, synced := range status.Synced {
		unstruct := &unstructured.Unstructured{}
		unstruct.SetName(synced.Name)
		unstruct.SetNamespace(synced.Namespace)
		unstruct.SetGroupVersionKind(
			schema.GroupVersionKind{
				Group:   synced.Group,
				Version: synced.Version,
				Kind:    synced.Kind,
			},
		)
		resourceInfo, err := clients.ResourceInfo(unstruct, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		current = append(current, resourceInfo)
	}

	if len(errs) > 0 {
		return current, types.NewMultiError(errs)
	}

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
	return target, nil
}

func resourcesClientSideApply(
	clients *moduleClient.SingletonClients,
	current, target kube.ResourceList,
) error {
	// Runtime Complexity of this Branch is 2N as Gets and Updates are sequential
	// this catches all resources in target that were created externally or if the synced
	// info is not up-to-date. If there is one missing in current its added.
	for i := range target {
		if target[i].Get() == nil && !current.Contains(target[i]) {
			current.Append(target[i])
		}
	}

	_, err := clients.KubeClient().Update(current, target, false)

	return err
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

	patch := func(info *resource.Info) {
		patchStart := time.Now()
		patchResults <- clients.Patch(ctx, info.Object.(*unstructured.Unstructured),
			client.Apply, client.ForceOwnership, owner,
		)
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf("updating %s", info.ObjectName()),
			"time", time.Now().Sub(patchStart))
	}

	for i := range resources {
		go patch(resources[i])
	}

	var errs []error
	for i := 0; i < len(patchResults); i++ {
		err := <-patchResults
		if err != nil {
			errs = append(errs, err)
		}
	}

	ssaFinish := time.Now().Sub(ssaStart)

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

func checkReady(ctx context.Context, clients *moduleClient.SingletonClients, target kube.ResourceList) (bool, error) {
	logger := log.FromContext(ctx)
	resourcesReady := true
	clientSet, _ := clients.KubernetesClientSet()
	checker := kube.NewReadyChecker(clientSet, func(format string, args ...interface{}) {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf(format, args...))
	}, kube.PausedAsReady(true), kube.CheckJobs(true))

	for i := range target {
		ready, err := checker.IsReady(ctx, target[i])
		if err != nil {
			return false, err
		}
		if !ready {
			resourcesReady = false
			break
		}
	}
	return resourcesReady, nil
}
