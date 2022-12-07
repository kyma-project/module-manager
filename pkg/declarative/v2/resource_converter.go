package v2

import (
	"github.com/kyma-project/module-manager/pkg/types"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
)

type ResourceInfoConverter interface {
	ResourceInfo(obj *unstructured.Unstructured, retryOnNoMatch bool) (*resource.Info, error)
}

type ResourceConverter interface {
	ConvertSyncedToNewStatus(status Status, resources []*resource.Info) Status
	ConvertStatusToResources(status Status) ([]*resource.Info, error)
	ConvertResourcesFromManifest(resources *types.ManifestResources) ([]*resource.Info, error)
}

func NewResourceConverter(converter ResourceInfoConverter, defaultNamespace string) ResourceConverter {
	return &defaultResourceConverter{converter: converter, defaultNamespace: defaultNamespace}
}

type defaultResourceConverter struct {
	converter        ResourceInfoConverter
	defaultNamespace string
}

func (c *defaultResourceConverter) ConvertSyncedToNewStatus(status Status, resources []*resource.Info) Status {
	status.Synced = make([]Resource, 0, len(resources))
	for _, info := range resources {
		status.Synced = append(
			status.Synced, Resource{
				Name:             info.Name,
				Namespace:        info.Namespace,
				GroupVersionKind: v1.GroupVersionKind(info.Mapping.GroupVersionKind),
			},
		)
	}
	return status
}

func (c *defaultResourceConverter) ConvertStatusToResources(status Status) ([]*resource.Info, error) {
	current := make([]*resource.Info, 0, len(status.Synced))
	errs := make([]error, 0, len(status.Synced))
	for _, synced := range status.Synced {
		resourceInfo, err := c.converter.ResourceInfo(synced.ToUnstructured(), true)
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

func (c *defaultResourceConverter) ConvertResourcesFromManifest(
	resources *types.ManifestResources,
) ([]*resource.Info, error) {
	target := make([]*resource.Info, 0, len(resources.Items))
	errs := make([]error, 0, len(resources.Items))
	for _, obj := range resources.Items {
		resourceInfo, err := c.converter.ResourceInfo(obj, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		target = append(target, resourceInfo)
	}
	if len(errs) > 0 {
		return nil, types.NewMultiError(errs)
	}
	c.normaliseNamespaces(target)
	return target, nil
}

// normaliseNamespaces is only a workaround for malformed resources, e.g. by bad charts or wrong type configs.
func (c *defaultResourceConverter) normaliseNamespaces(infos []*resource.Info) {
	for _, info := range infos {
		obj, ok := info.Object.(v1.Object)
		if !ok {
			continue
		}
		if info.Namespaced() {
			if info.Namespace == "" || obj.GetNamespace() == "" {
				info.Namespace = c.defaultNamespace
				obj.SetNamespace(c.defaultNamespace)
			}
		} else {
			if info.Namespace != "" || obj.GetNamespace() != "" {
				info.Namespace = ""
				obj.SetNamespace("")
			}
		}
	}
}
