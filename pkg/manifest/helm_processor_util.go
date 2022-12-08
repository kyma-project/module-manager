package manifest

import (
	"context"

	"errors"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
)

var ErrResourceNotReady = errors.New("resource not ready")
var ErrResourceNotDeleted = errors.New("resource not deleted")

func checkResourcesDeleted(targetResources kube.ResourceList) error {
	return targetResources.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		err = info.Get()
		if err == nil {
			return ErrResourceNotDeleted
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	})
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

func checkReady(ctx context.Context, resourceList kube.ResourceList, readyChecker kube.ReadyChecker) error {
	return resourceList.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		ready, err := readyChecker.IsReady(ctx, info)
		if !ready {
			return ErrResourceNotReady
		}
		if err != nil {
			return err
		}
		return nil
	})
}
