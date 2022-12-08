package manifest_test

import (
	"errors"
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/kyma-project/module-manager/pkg/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cp "github.com/otiai10/copy"
	"os"
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


var _ = Describe("given InstallInfo with a Kustomize location", func() {
	installInfo := getDeployInfo("testResourceName", "cacheKey",
		copyAndWriteToTemp("../test_samples/kustomize"), "", types.ChartFlags{})
	DescribeTable("Test Cache",
		func(givenCondition func() error, expectedBehavior func() bool) {
			Eventually(givenCondition, Timeout, Interval).Should(Succeed())
			Eventually(expectedBehavior, Timeout, Interval).Should(BeTrue())
		},
		Entry("When InstallInfo.DisableCache=true, expect cached manifest.yaml not exists",
			GetManifest(&installInfo, true), ExpectManifestYamlExists(&installInfo, false)),
		Entry("When InstallInfo.DisableCache=false, expect cached manifest.yaml exists",
			GetManifest(&installInfo, false), ExpectManifestYamlExists(&installInfo, true)),
	)

})

func GetManifest(deployInfo *types.InstallInfo, disableCache bool) func() error {
	return func() error {
		deployInfo.DisableCache = disableCache
		operations, err := manifest.NewOperations(logger, *deployInfo, nil, nil)
		_ = operations.GetManifestForChartPath(*deployInfo)
		return err
	}
}

func copyAndWriteToTemp(srcPath string) string {
	temp, err := os.MkdirTemp("", "temp*")
	Expect(err).ShouldNot(HaveOccurred())
	err = cp.Copy(srcPath, temp)
	Expect(err).ShouldNot(HaveOccurred())
	return temp
}

func ExpectManifestYamlExists(deployInfo *types.InstallInfo, exists bool) func() bool {
	return func() bool {
		_, err := os.Stat(util.GetFsManifestChartPath(deployInfo.ChartPath))
		if exists {
			return err == nil
		}
		return err != nil
	}
}
