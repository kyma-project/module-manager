package manifest_test

import (
	"errors"
	"testing"

	"github.com/kyma-project/module-manager/pkg/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_UninstallSuccess(t *testing.T) {
	t.Parallel()
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "when no error, expect uninstall successfully",
			args: args{err: nil},
			want: true,
		},
		{
			name: "when api not found error, expect uninstall successfully",
			args: args{err: &apierrors.StatusError{ErrStatus: metav1.Status{
				Status: metav1.StatusFailure,
				Reason: metav1.StatusReasonNotFound,
			}}},
			want: true,
		},
		{
			name: "when api no kind match error, expect uninstall successfully",
			args: args{err: &apimeta.NoKindMatchError{}},
			want: true,
		},
		{
			name: "when api no resource match error, expect uninstall successfully",
			args: args{err: &apimeta.NoResourceMatchError{}},
			want: true,
		},
		{
			name: "when receive normal error, expect uninstall successfully",
			args: args{err: errors.New("some unexpected error")},
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := manifest.UninstallSuccess(tt.args.err); got != tt.want {
				t.Errorf("UninstallSuccess() = %v, want %v", got, tt.want)
			}
		})
	}
}
