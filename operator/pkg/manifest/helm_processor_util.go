package manifest

import (
	"context"

	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
)

func checkResourcesDeleted(targetResources kube.ResourceList) (bool, error) {
	resourcesDeleted := true
	err := targetResources.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		err = info.Get()
		if err == nil || !apierrors.IsNotFound(err) {
			resourcesDeleted = false
			return err
		}
		return nil
	})
	return resourcesDeleted, err
}

func setNamespaceIfNotPresent(targetNamespace string, resourceInfo *resource.Info,
	helper *resource.Helper, runtimeObject runtime.Object,
) error {
	// check if resource is scoped to namespaces
	if helper.NamespaceScoped && resourceInfo.Namespace == "" {
		// check existing namespace - continue only if not set
		if targetNamespace == "" {
			targetNamespace = v1.NamespaceDefault
		}

		// set namespace on request
		resourceInfo.Namespace = targetNamespace
		if _, err := meta.Accessor(runtimeObject); err != nil {
			return err
		}

		// set namespace on runtime object
		return accessor.SetNamespace(runtimeObject, targetNamespace)
	}
	return nil
}

func overrideNamespace(resourceList kube.ResourceList, targetNamespace string) error {
	return resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		helper := resource.NewHelper(info.Client, info.Mapping)
		return setNamespaceIfNotPresent(targetNamespace, info, helper, info.Object)
	})
}

func checkReady(ctx context.Context, resourceList kube.ResourceList,
	readyChecker kube.ReadyChecker,
) (bool, error) {
	resourcesReady := true
	err := resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		if ready, err := readyChecker.IsReady(ctx, info); !ready || err != nil {
			resourcesReady = ready
			return err
		}
		return nil
	})
	return resourcesReady, err
}
