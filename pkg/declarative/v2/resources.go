package v2

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/imdario/mergo"
	moduleClient "github.com/kyma-project/module-manager/pkg/client"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	machineryTypes "k8s.io/apimachinery/pkg/types"
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

func resourcesPatch(
	ctx context.Context,
	clients *moduleClient.SingletonClients,
	owner client.FieldOwner,
	resources []*resource.Info,
	ssa bool,
) error {
	ssaStart := time.Now()
	logger := log.FromContext(ctx, "owner", owner)
	logger.V(util.DebugLogLevel).Info("ResourcePatch", "resources", len(resources))

	// Runtime Complexity of this Branch is N as only ServerSideApplier Patch is required
	patchResults := make(chan error, len(resources))

	patch := func(ctx context.Context, info *resource.Info) {
		patchStart := time.Now()

		var patchType machineryTypes.PatchType
		if ssa {
			patchType = machineryTypes.ApplyPatchType
		} else {
			patchType = machineryTypes.MergePatchType
		}
		logger.V(util.DebugLogLevel).Info(
			fmt.Sprintf("patch %s (%s)", info.ObjectName(), info.Mapping.GroupVersionKind),
			"patchType", patchType)
		patchResults <- patchInfo(ctx, clients.Client, info, patchType, owner)
		logger.V(util.DebugLogLevel).Info(
			fmt.Sprintf("patch for %s (%s) finished", info.ObjectName(), info.Mapping.GroupVersionKind),
			"patchType", patchType,
			"time", time.Since(patchStart))
	}

	for i := range resources {
		i := i
		patch(ctx, resources[i])
	}

	var errs []error
	for i := 0; i < len(resources); i++ {
		if err := <-patchResults; err != nil {
			errs = append(errs, err)
		}
	}

	ssaFinish := time.Since(ssaStart)

	if errs != nil {
		return fmt.Errorf("ResourcePatch failed (after %s): %w", ssaFinish, types.NewMultiError(errs))
	}
	logger.V(util.DebugLogLevel).Info("ResourcePatch finished", "time", ssaFinish)

	return nil
}

func patchInfo(
	ctx context.Context,
	clnt client.Client,
	info *resource.Info,
	patchType machineryTypes.PatchType,
	owner client.FieldOwner,
) error {
	logger := log.FromContext(ctx, "patchType", patchType)

	info.Object = convertUnstructuredToTyped(clnt, info.Object, info.Mapping)

	obj, isTyped := info.Object.(client.Object)
	if !isTyped {
		return fmt.Errorf("client object conversion in SSA for %s failed", info.ObjectName())
	}

	var err error
	switch patchType {
	case machineryTypes.ApplyPatchType:
		err = clnt.Patch(ctx, obj, client.Apply, client.ForceOwnership, owner)
	case machineryTypes.MergePatchType:
		unstruct := &unstructured.Unstructured{}
		unstruct.SetGroupVersionKind(info.Mapping.GroupVersionKind)
		onCluster := convertUnstructuredToTyped(clnt, unstruct, info.Mapping).(client.Object)
		//Backwards compatible with SSA
		err := clnt.Get(ctx, client.ObjectKeyFromObject(obj), onCluster)
		if apierrors.IsNotFound(err) {
			return clnt.Create(ctx, obj, owner)
		}
		if err != nil {
			return fmt.Errorf("getting original object (merge) for %s (%s) failed: %w",
				info.ObjectName(), info.Mapping.GroupVersionKind, err)
		}
		normaliseAPIVersionKind(obj, onCluster)
		obj.SetCreationTimestamp(onCluster.GetCreationTimestamp())
		obj.SetManagedFields(onCluster.GetManagedFields())
		obj.SetUID(onCluster.GetUID())
		obj.SetResourceVersion(onCluster.GetResourceVersion())
		localMerge := onCluster.DeepCopyObject()
		if err := mergo.Merge(localMerge, obj, mergo.WithOverride); err != nil {
			return err
		}
		var patch []byte

		if _, isUnstructured := obj.(runtime.Unstructured); isUnstructured {
			patch, err = client.MergeFrom(onCluster).Data(localMerge.(client.Object))
		} else {
			patch, err = client.StrategicMergeFrom(onCluster).Data(localMerge.(client.Object))
		}

		if err != nil {
			return fmt.Errorf("creating patch for %s (%s) failed: %w",
				info.ObjectName(), info.Mapping.GroupVersionKind, err)
		}

		if len(patch) == 0 || string(patch) == "{}" {
			logger.V(util.DebugLogLevel).Info("patch skipped")
			return nil
		}

		err = clnt.Patch(ctx, obj, client.RawPatch(patchType, patch), owner)
	}

	if err != nil {
		return fmt.Errorf("patch for %s (%s) failed: %w", info.ObjectName(),
			info.Mapping.GroupVersionKind, err)
	}

	return nil
}

func normaliseAPIVersionKind(from, to client.Object) {
	typeMeta, err := meta.TypeAccessor(from)
	if err != nil {
		return
	}
	newTypeMeta, err := meta.TypeAccessor(to)
	if err != nil {
		return
	}
	newTypeMeta.SetKind(typeMeta.GetKind())
	newTypeMeta.SetAPIVersion(typeMeta.GetAPIVersion())
}

// convertWithMapper converts the given object with the optional provided
// RESTMapping. If no mapping is provided, the default schema versioner is used
func convertUnstructuredToTyped(clnt client.Client, obj runtime.Object, mapping *meta.RESTMapping) runtime.Object {
	s := clnt.Scheme()
	var gv = runtime.GroupVersioner(schema.GroupVersions(s.PrioritizedVersionsAllGroups()))
	if mapping != nil {
		gv = mapping.GroupVersionKind.GroupVersion()
	}
	if obj, err := s.UnsafeConvertToVersion(obj, gv); err == nil {
		return obj
	}
	return obj
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
