package manifest_test

import (
	"os"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

const (
	testResourceName  = "testBaseResource"
	testResourceName2 = "testBaseResource2"
	testResourceName3 = "testBaseResource3"
	testNs            = "testNs"
	parentCacheKey    = "test-parent-name"
	parentCacheKey2   = "test-parent-name2"
)

var (
	rendererCache     types.RendererCache //nolint:gochecknoglobals
	setProcessorCount = 0                 //nolint:gochecknoglobals
	setConfigCount    = 0                 //nolint:gochecknoglobals
	//nolint:gochecknoglobals
	chartFlagsVariantOne = types.ChartFlags{
		ConfigFlags: types.Flags{
			"key1": "value1",
		},
	}
	//nolint:gochecknoglobals
	chartFlagsVariantTwo = types.ChartFlags{
		ConfigFlags: types.Flags{
			"key2": "value2",
		},
	}
)

func getDeployInfo(resourceName, parentCacheKey, chartPath, url string, flags types.ChartFlags) types.InstallInfo {
	return types.InstallInfo{
		ChartInfo: &types.ChartInfo{
			ChartPath: chartPath,
			URL:       url,
			RepoName:  "someRepoName",
			Flags:     flags,
			ChartName: "someChartName",
		},
		ClusterInfo: types.ClusterInfo{
			Config: &rest.Config{},
		},
		ResourceInfo: types.ResourceInfo{
			BaseResource: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      resourceName,
						"namespace": testNs,
						"labels": map[string]interface{}{
							labels.CacheKey: parentCacheKey,
						},
					},
				},
			},
		},
	}
}

func remoteHelm() func(resourceName string, parentKey string, flags types.ChartFlags) (string, string, uint32) {
	return func(resourceName string, parentKey string, flags types.ChartFlags) (string, string, uint32) {
		_, err := manifest.NewOperations(&logger, getDeployInfo(resourceName, parentKey, "",
			"https://helm.nginx.com/stable", flags), nil, rendererCache)
		Expect(err).ShouldNot(HaveOccurred())
		flagsHash, err := util.CalculateHash(flags)
		Expect(err).ShouldNot(HaveOccurred())
		return resourceName, parentKey, flagsHash
	}
}

func localHelm() func(resourceName string, parentKey string, flags types.ChartFlags) (string, string, uint32) {
	return func(resourceName string, parentKey string, flags types.ChartFlags) (string, string, uint32) {
		_, err := manifest.NewOperations(&logger, getDeployInfo(resourceName, parentKey,
			"../test_samples/helm", "", flags), nil, rendererCache)
		Expect(err).ShouldNot(HaveOccurred())
		flagsHash, err := util.CalculateHash(flags)
		Expect(err).ShouldNot(HaveOccurred())
		return resourceName, parentKey, flagsHash
	}
}

func verifyCacheEntries(resourceName string, parentKeyName string, flagVariantHash uint32) {
	Expect(rendererCache.GetProcessor(client.ObjectKey{Name: parentKeyName, Namespace: testNs})).
		ShouldNot(BeNil())
	Expect(rendererCache.GetConfig(client.ObjectKey{Name: resourceName, Namespace: testNs})).
		Should(Equal(flagVariantHash))
}

type mockCache struct {
	processor sync.Map
	config    sync.Map
}

func (m *mockCache) GetProcessor(key client.ObjectKey) types.RenderSrc {
	value, ok := m.processor.Load(key)
	if !ok {
		return nil
	}
	return value.(types.RenderSrc)
}

func (m *mockCache) SetProcessor(key client.ObjectKey, renderSrc types.RenderSrc) {
	m.processor.Store(key, renderSrc)
	setProcessorCount++
}

func (m *mockCache) DeleteProcessor(key client.ObjectKey) {
	m.processor.Delete(key)
}

func (m *mockCache) GetConfig(key client.ObjectKey) uint32 {
	value, ok := m.config.Load(key)
	if !ok {
		return 0
	}
	return value.(uint32)
}

func (m *mockCache) SetConfig(key client.ObjectKey, cfg uint32) {
	m.config.Store(key, cfg)
	setConfigCount++
}

func (m *mockCache) DeleteConfig(key client.ObjectKey) {
	m.config.Delete(key)
}

var _ = Describe("given manifest with a helm repo", Ordered, func() {
	BeforeAll(func() {
		rendererCache = &mockCache{
			processor: sync.Map{},
			config:    sync.Map{},
		}
	})
	BeforeEach(func() {
		// delete processor entry for parent key
		rendererCache.DeleteProcessor(client.ObjectKey{Name: parentCacheKey, Namespace: testNs})
		rendererCache.DeleteProcessor(client.ObjectKey{Name: parentCacheKey2, Namespace: testNs})
		// delete processor entry for child key
		rendererCache.DeleteConfig(client.ObjectKey{Name: testResourceName, Namespace: testNs})
		rendererCache.DeleteConfig(client.ObjectKey{Name: testResourceName2, Namespace: testNs})
		rendererCache.DeleteConfig(client.ObjectKey{Name: testResourceName3, Namespace: testNs})
		// reset set counters
		setConfigCount = 0
		setProcessorCount = 0
		// clear cached manifests
		os.RemoveAll(util.GetFsManifestChartPath("../test_samples/kustomize"))
		os.RemoveAll(util.GetFsManifestChartPath("../test_samples/helm"))
	})

	DescribeTable("given renderer cache for manifest processing",
		func(testCaseFn func(resourceName string, parentKey string, flags types.ChartFlags) (string, string, uint32)) {
			// first call for operations for same parent resource and configuration
			verifyCacheEntries(testCaseFn(testResourceName, parentCacheKey, chartFlagsVariantOne))
			// second call for operations for same parent resource and configuration
			verifyCacheEntries(testCaseFn(testResourceName, parentCacheKey, chartFlagsVariantOne))
			// third call for operations for same parent resource and configuration
			verifyCacheEntries(testCaseFn(testResourceName, parentCacheKey, chartFlagsVariantOne))
			// fourth call for operations for same parent resource and configuration
			verifyCacheEntries(testCaseFn(testResourceName, parentCacheKey, chartFlagsVariantOne))
			// check set count
			Expect(setProcessorCount).To(Equal(2)) // new processor + new flags
			Expect(setConfigCount).To(Equal(1))    // new flags

			// fifth call for operations for same parent resource and DIFFERENT configuration
			verifyCacheEntries(testCaseFn(testResourceName, parentCacheKey, chartFlagsVariantTwo))
			// check set count
			Expect(setProcessorCount).To(Equal(3)) // ^above + updated flags
			Expect(setConfigCount).To(Equal(2))    // ^above + updated flags

			// sixth call for operations for same parent DIFFERENT resource and configuration
			verifyCacheEntries(testCaseFn(testResourceName2, parentCacheKey, chartFlagsVariantOne))
			// check set count
			Expect(setProcessorCount).To(Equal(4)) // ^above + flags of new resource
			Expect(setConfigCount).To(Equal(3))    // ^above + flags of new resource

			// seventh call for operations for a resource and new parent and configuration
			verifyCacheEntries(testCaseFn(testResourceName3, parentCacheKey2, chartFlagsVariantOne))
			// check set count
			Expect(setProcessorCount).To(Equal(6)) // ^above + new parent + new flags
			Expect(setConfigCount).To(Equal(4))    // ^above + new flags
		},
		[]TableEntry{
			Entry("when local helm chart path is provided", remoteHelm()),
			Entry("when local kustomize chart is provided", localHelm()),
		})
})
