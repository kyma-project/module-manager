package v2

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/kyma-project/module-manager/internal"
	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ManifestParser interface {
	Parse(ctx context.Context, renderer Renderer, obj Object, spec *Spec) (*types.ManifestResources, error)
}

func NewInMemoryCachedManifestParser(ttl time.Duration) *InMemoryManifestCache {
	cache := ttlcache.New[string, types.ManifestResources]()
	return &InMemoryManifestCache{Cache: cache, TTL: ttl}
}

type InMemoryManifestCache struct {
	TTL time.Duration
	*ttlcache.Cache[string, types.ManifestResources]
}

func (c *InMemoryManifestCache) Parse(
	ctx context.Context, renderer Renderer, obj Object, spec *Spec,
) (*types.ManifestResources, error) {
	file := filepath.Join(manifest, spec.Path, spec.ManifestName)
	hashedValues, _ := internal.CalculateHash(spec.Values)
	hash := fmt.Sprintf("%v", hashedValues)
	key := fmt.Sprintf("%s-%s-%s", file, spec.Mode, hash)

	item := c.Cache.Get(key)
	if item != nil {
		resources := item.Value()

		copied := &types.ManifestResources{
			Items: make([]*unstructured.Unstructured, 0, len(resources.Items)),
			Blobs: resources.Blobs,
		}
		for _, res := range resources.Items {
			copied.Items = append(copied.Items, res.DeepCopy())
		}

		return copied, nil
	}

	rendered, err := renderer.Render(ctx, obj)
	if err != nil {
		return nil, err
	}

	resources, err := internal.ParseManifestStringToObjects(string(rendered))
	if err != nil {
		return nil, err
	}

	c.Cache.Set(key, *resources, c.TTL)

	copied := &types.ManifestResources{
		Items: make([]*unstructured.Unstructured, 0, len(resources.Items)),
		Blobs: resources.Blobs,
	}
	for _, res := range resources.Items {
		copied.Items = append(copied.Items, res.DeepCopy())
	}

	return copied, nil
}
