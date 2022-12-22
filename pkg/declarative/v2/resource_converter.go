package v2

import (
	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
)

type ResourceInfoConverter interface {
	ResourceInfo(obj *unstructured.Unstructured, retryOnNoMatch bool) (*resource.Info, error)
}

type ResourceToInfoConverter interface {
	ResourcesToInfos([]Resource) ([]*resource.Info, error)
	UnstructuredToInfos([]*unstructured.Unstructured) ([]*resource.Info, error)
}

type InfoToResourceConverter interface {
	InfosToResources([]*resource.Info) []Resource
}

func NewResourceToInfoConverter(converter ResourceInfoConverter, defaultNamespace string) ResourceToInfoConverter {
	return &defaultResourceToInfoConverter{converter: converter, defaultNamespace: defaultNamespace}
}

type defaultResourceToInfoConverter struct {
	converter        ResourceInfoConverter
	defaultNamespace string
}

func NewInfoToResourceConverter() InfoToResourceConverter {
	return &defaultInfoToResourceConverter{}
}

type defaultInfoToResourceConverter struct{}

func (c *defaultInfoToResourceConverter) InfosToResources(infos []*resource.Info) []Resource {
	resources := make([]Resource, 0, len(infos))
	for _, info := range infos {
		var gvk v1.GroupVersionKind
		if info.Mapping != nil {
			gvk = v1.GroupVersionKind(info.ResourceMapping().GroupVersionKind)
		} else {
			gvk = v1.GroupVersionKind(info.Object.GetObjectKind().GroupVersionKind())
		}
		resources = append(
			resources, Resource{
				Name:             info.Name,
				Namespace:        info.Namespace,
				GroupVersionKind: gvk,
			},
		)
	}
	return resources
}

func (c *defaultResourceToInfoConverter) ResourcesToInfos(resources []Resource) ([]*resource.Info, error) {
	current := make([]*resource.Info, 0, len(resources))
	errs := make([]error, 0, len(resources))
	for _, res := range resources {
		resourceInfo, err := c.converter.ResourceInfo(res.ToUnstructured(), true)
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

func (c *defaultResourceToInfoConverter) UnstructuredToInfos(
	resources []*unstructured.Unstructured,
) ([]*resource.Info, error) {
	target := make([]*resource.Info, 0, len(resources))
	errs := make([]error, 0, len(resources))
	for _, obj := range resources {
		resourceInfo, err := c.converter.ResourceInfo(obj, true)

		// if there is no match we will initialize the resource anyway, just without
		// the mapping. This will cause the applier and mappings to fall back to unstructured
		// if this apply fails, it will continue to fail until either the mapping is resolved
		// correctly or the kind is present
		if meta.IsNoMatchError(err) {
			target = append(
				target, &resource.Info{
					Namespace:       obj.GetNamespace(),
					Name:            obj.GetName(),
					Object:          obj,
					ResourceVersion: obj.GetResourceVersion(),
				},
			)
			continue
		}

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
func (c *defaultResourceToInfoConverter) normaliseNamespaces(infos []*resource.Info) {
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
