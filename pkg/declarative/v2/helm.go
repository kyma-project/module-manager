package v2

import (
	"context"
	"fmt"

	"github.com/kyma-project/module-manager/pkg/types"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

const (
	ConditionTypeHelmCRDs               ConditionType   = "HelmCRDs"
	ConditionReasonHelmCRDsAreAvailable ConditionReason = "HelmCRDsAvailable"
)

func NewHelmRenderer(
	spec *Spec,
	clnt Client,
	options *Options,
) Renderer {
	valuesAsMap, ok := spec.Values.(map[string]any)
	if !ok {
		valuesAsMap = map[string]any{}
	}
	return &Helm{
		recorder:   options.EventRecorder,
		chartPath:  spec.Path,
		values:     valuesAsMap,
		clnt:       clnt,
		crdChecker: NewHelmReadyCheck(clnt),
	}
}

type Helm struct {
	recorder record.EventRecorder
	clnt     Client

	chartPath string
	values    map[string]any

	chart *chart.Chart
	crds  kube.ResourceList

	crdChecker ReadyCheck
}

func (h *Helm) prerequisiteCondition(object metav1.Object) metav1.Condition {
	return metav1.Condition{
		Type:               string(ConditionTypeHelmCRDs),
		Reason:             string(ConditionReasonHelmCRDsAreAvailable),
		Status:             metav1.ConditionFalse,
		Message:            "CustomResourceDefinitions from chart 'crds' folder are installed and ready for use",
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

	crds, err := getCRDs(h.clnt, h.chart.CRDObjects())
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

	if err := installCRDs(h.clnt, h.crds); err != nil {
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

	restMapper, _ := h.clnt.ToRESTMapper()
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
	if deleted, err := ConcurrentCleanup(h.clnt).Run(ctx, h.crds); err != nil {
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
	release, err := h.clnt.Install().RunWithContext(ctx, h.chart, h.values)
	if err != nil {
		h.recorder.Event(obj, "Warning", "HelmRenderRun", err.Error())
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
		return nil, err
	}
	return []byte(release.Manifest), nil
}
