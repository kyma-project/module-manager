package v2

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kyma-project/module-manager/pkg/client"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	ErrConditionsNotYetRegistered = errors.New("conditions have not yet been registered in status")
	ErrPrerequisitesNotFulfilled  = errors.New("prerequisites for installation are not fulfilled")
	ErrPrerequisitesNotRemoved    = errors.New("prerequisites for installation are not removed yet")
)

type Prerequisites []*resource.Info

type ConditionsNeedUpdate bool

type Renderer interface {
	Initialize(obj Object) error

	Render(ctx context.Context, obj Object) ([]byte, error)

	EnsurePrerequisites(ctx context.Context, obj Object) error
	RemovePrerequisites(ctx context.Context, obj Object) error
}

type HelmWithCache struct {
	*Helm
	*manifestCache
}

type Helm struct {
	recorder record.EventRecorder
	clients  *client.SingletonClients

	chartPath string
	values    map[string]any

	chart *chart.Chart
	crds  kube.ResourceList

	crdChecker ReadyCheck
}

func NewHelmRendererWithCache(
	spec *ManifestSpec,
	clients *client.SingletonClients,
	options *Options,
) Renderer {
	return &HelmWithCache{
		Helm: &Helm{
			recorder:   options.EventRecorder,
			chartPath:  spec.ChartPath,
			values:     spec.Values,
			clients:    clients,
			crdChecker: NewHelmReadyCheck(clients),
		},
		manifestCache: newManifestCache(options.ManifestCacheRoot, spec),
	}
}

func (h *Helm) prerequisiteCondition(object metav1.Object) metav1.Condition {
	return metav1.Condition{
		Type:               string(ConditionTypeCRDs),
		Reason:             string(ConditionReasonCRDsAreAvailable),
		Status:             metav1.ConditionFalse,
		Message:            "CustomResourceDefinitions are installed and ready for use",
		ObservedGeneration: object.GetGeneration(),
	}
}

func (h *Helm) Initialize(obj Object) error {
	status := obj.GetStatus()

	prerequisiteExists := meta.FindStatusCondition(status.Conditions, h.prerequisiteCondition(obj).Type) != nil
	if !prerequisiteExists {
		status := obj.GetStatus()
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status)
		return ErrConditionsNotYetRegistered
	}

	loadedChart, err := loader.Load(h.chartPath)
	if err != nil {
		h.recorder.Event(obj, "Warning", "ChartLoading", err.Error())
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return err
	}
	h.chart = loadedChart

	crds, err := getCRDs(h.clients, h.chart.CRDObjects())
	if err != nil {
		h.recorder.Event(obj, "Warning", "CRDParsing", err.Error())
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return err
	}
	h.crds = crds

	return nil
}

func (h *Helm) EnsurePrerequisites(ctx context.Context, obj Object) error {
	status := obj.GetStatus()

	if obj.GetDeletionTimestamp().IsZero() && meta.IsStatusConditionTrue(
		status.Conditions, h.prerequisiteCondition(obj).Type,
	) {
		return nil
	}

	if err := installCRDs(h.clients, h.crds); err != nil {
		h.recorder.Event(obj, "Warning", "CRDInstallation", err.Error())
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return fmt.Errorf("crds could not be installed: %w", err)
	}

	crdsReady, err := h.crdChecker.Run(ctx, h.crds)
	if err != nil {
		h.recorder.Event(obj, "Warning", "CRDReadyCheck", err.Error())
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return fmt.Errorf("crds could not be checked for readiness: %w", err)
	}

	if !crdsReady {
		h.recorder.Event(obj, "Normal", "CRDReadyCheck", "crds are not yet ready...")
		meta.SetStatusCondition(&status.Conditions, h.prerequisiteCondition(obj))
		obj.SetStatus(status.WithErr(ErrPrerequisitesNotFulfilled))
		return fmt.Errorf("crds are not yet ready: %w", ErrPrerequisitesNotFulfilled)
	}

	restMapper, _ := h.clients.ToRESTMapper()
	meta.MaybeResetRESTMapper(restMapper)
	cond := h.prerequisiteCondition(obj)
	cond.Status = metav1.ConditionTrue
	h.recorder.Event(obj, "Normal", cond.Reason, cond.Message)
	meta.SetStatusCondition(&status.Conditions, cond)
	obj.SetStatus(status.WithOperation("CRDs are ready"))

	return nil
}

func (h *Helm) RemovePrerequisites(ctx context.Context, obj Object) error {
	status := obj.GetStatus()
	if deleted, err := ConcurrentCleanup(h.clients).Run(ctx, h.crds); err != nil {
		h.recorder.Event(obj, "Warning", "CRDsUninstallation", err.Error())
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return err
	} else if !deleted {
		waitingMsg := "waiting for crds to be uninstalled"
		h.recorder.Event(obj, "Normal", "CRDsUninstallation", waitingMsg)
		obj.SetStatus(status.WithOperation(waitingMsg))
		return fmt.Errorf("crds are not yet deleted: %w", ErrPrerequisitesNotRemoved)
	}
	return nil
}

func (h *Helm) Render(ctx context.Context, obj Object) ([]byte, error) {
	status := obj.GetStatus()
	release, err := h.clients.Install().RunWithContext(ctx, h.chart, h.values)
	if err != nil {
		h.recorder.Event(obj, "Warning", "HelmRenderRun", err.Error())
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return nil, err
	}
	return []byte(release.Manifest), nil
}

func (h *HelmWithCache) Render(ctx context.Context, obj Object) ([]byte, error) {
	logger := log.FromContext(ctx)
	status := obj.GetStatus()

	if err := h.Clean(); err != nil {
		err := fmt.Errorf("cleaning cache failed: %w", err)
		h.recorder.Event(obj, "Warning", "ManifestCacheCleanup", err.Error())
		obj.SetStatus(status.WithState(StateError).WithErr(err))
		return nil, err
	}

	cacheFile := h.ReadYAML()

	if cacheFile.GetRawError() != nil {
		renderStart := time.Now()
		logger.Info(
			"no cached manifest, rendering again",
			"hash", h.hash,
			"path", h.manifestCache.String(),
		)
		manifest, err := h.Helm.Render(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("rendering new manifest failed: %w", err)
		}
		logger.Info("rendering finished", "time", time.Since(renderStart))
		if err := util.WriteToFile(h.manifestCache.String(), manifest); err != nil {
			h.recorder.Event(obj, "Warning", "ManifestWriting", err.Error())
			obj.SetStatus(status.WithState(StateError).WithErr(err))
			return nil, err
		}
		return manifest, nil
	}

	logger.V(util.DebugLogLevel).Info("reuse manifest from cache", "hash", h.manifestCache.hash)

	return []byte(cacheFile.GetContent()), nil
}
