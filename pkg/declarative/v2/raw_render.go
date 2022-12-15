package v2

import (
	"context"
	"os"

	"github.com/kyma-project/module-manager/pkg/types"
	"k8s.io/client-go/tools/record"
)

func NewRawRenderer(
	spec *ManifestSpec,
	options *Options,
) Renderer {
	return &RawRenderer{
		recorder: options.EventRecorder,
		path:     spec.Path,
	}
}

type RawRenderer struct {
	recorder record.EventRecorder
	path     string
}

func (r *RawRenderer) Initialize(_ Object) error {
	return nil
}

func (r *RawRenderer) EnsurePrerequisites(_ context.Context, _ Object) error {
	return nil
}

func (r *RawRenderer) Render(_ context.Context, obj Object) ([]byte, error) {
	status := obj.GetStatus()
	manifest, err := os.ReadFile(r.path)
	if err != nil {
		r.recorder.Event(obj, "Warning", "ReadRawManifest", err.Error())
		obj.SetStatus(status.WithState(State(types.StateError)).WithErr(err))
	}
	return manifest, nil
}

func (r *RawRenderer) RemovePrerequisites(_ context.Context, _ Object) error {
	return nil
}
