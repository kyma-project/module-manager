package declarative

import (
	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
)

func TestGet(t *testing.T) {
	type test struct {
		name                     string
		object                   types.BaseCustomObject
		expectedInstallationSpec types.InstallationSpec
		expectedErr              error
	}

	tests := []test{
		{
			name: "",
			object: &TestCRD{
				Spec: types.InstallationSpec{
					ChartPath:   "path/to/chart",
					ReleaseName: "test-release",
					ChartFlags:  types.ChartFlags{},
				},
			},
			expectedInstallationSpec: types.InstallationSpec{},
			expectedErr:              nil,
		},
		{
			name:                     "",
			object:                   nil,
			expectedInstallationSpec: types.InstallationSpec{},
			expectedErr:              nil,
		},
		{
			name:                     "",
			object:                   nil,
			expectedInstallationSpec: types.InstallationSpec{},
			expectedErr:              nil,
		},
	}

	for _, tc := range tests {
		resolver := DefaultManifestResolver{}
		logger := logr.Logger{}
		t.Run(tc.name, func(t *testing.T) {
			installationSpec, err := resolver.Get(tc.object, logger)
			assert.Equal(t, tc.expectedInstallationSpec, installationSpec)
			assert.Equal(t, tc.expectedErr, err)
		})
	}
}

// TestCRD implements the BaseCustomObject and can be used for easy testing
type TestCRD struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   types.InstallationSpec `json:"spec,omitempty"`
	Status types.Status           `json:"status,omitempty"`
}

func (s *TestCRD) GetStatus() types.Status {
	//return s.Status
	return types.Status{}
}

func (s *TestCRD) SetStatus(status types.Status) {
	//s.Status = status
}

func (s *TestCRD) ComponentName() string {
	return "test-component-name"
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TestCRD) DeepCopyInto(out *TestCRD) {
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TestCRD.
func (in *TestCRD) DeepCopy() *TestCRD {
	return nil
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *TestCRD) DeepCopyObject() runtime.Object {
	return nil
}
