package manifest_test

import (
	"github.com/kyma-project/module-manager/pkg/manifest"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cp "github.com/otiai10/copy"
	"os"
)

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
