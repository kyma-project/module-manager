package v2

import (
	"context"
	"fmt"
	"time"

	"github.com/kyma-project/module-manager/pkg/util"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func WrapWithRendererCache(
	renderer Renderer,
	spec *Spec,
	options *Options,
) Renderer {
	if options.ManifestCache == NoManifestCache {
		return renderer
	}

	return &RendererWithCache{
		Renderer:      renderer,
		recorder:      options.EventRecorder,
		manifestCache: newManifestCache(string(options.ManifestCache), spec),
	}
}

type RendererWithCache struct {
	Renderer
	recorder record.EventRecorder
	*manifestCache
}

func (k *RendererWithCache) Render(ctx context.Context, obj Object) ([]byte, error) {
	logger := log.FromContext(ctx)
	status := obj.GetStatus()

	if err := k.Clean(); err != nil {
		err := fmt.Errorf("cleaning cache failed: %w", err)
		k.recorder.Event(obj, "Warning", "ManifestCacheCleanup", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return nil, err
	}

	cacheFile := k.ReadYAML()

	if cacheFile.GetRawError() != nil {
		renderStart := time.Now()
		logger.Info(
			"no cached manifest, rendering again",
			"hash", k.hash,
			"path", k.manifestCache.String(),
		)
		manifest, err := k.Renderer.Render(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("rendering new manifest failed: %w", err)
		}
		logger.Info("rendering finished", "time", time.Since(renderStart))
		if err := util.WriteToFile(k.manifestCache.String(), manifest); err != nil {
			k.recorder.Event(obj, "Warning", "ManifestWriting", err.Error())
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return nil, err
		}
		return manifest, nil
	}

	logger.V(util.DebugLogLevel).Info("reuse manifest from cache", "hash", k.manifestCache.hash)

	return []byte(cacheFile.GetContent()), nil
}
