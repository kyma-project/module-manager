package controllers_test

import (
	"encoding/json"

	"os"

	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"errors"
	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

const (
	Timeout  = time.Second * 30
	Interval = time.Millisecond * 250
)

var (
	ErrManifestStateMisMatch = errors.New("ManifestState mismatch")
)

func getManifestState(manifestName string) v1alpha1.ManifestState {
	manifest := &v1alpha1.Manifest{}

	err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: v1.NamespaceDefault,
		Name:      manifestName,
	}, manifest)
	if err != nil {
		return "invalid"
	}
	return manifest.Status.State
}

func getManifest(key client.ObjectKey) func() bool {
	return func() bool {
		manifest := v1alpha1.Manifest{}
		err := k8sClient.Get(ctx, key, &manifest)
		return apiErrors.IsNotFound(err)
	}
}

func setHelmEnv() error {
	os.Setenv(helmCacheHomeEnv, helmCacheHome)
	os.Setenv(helmCacheRepoEnv, helmCacheRepo)
	os.Setenv(helmRepoEnv, helmRepoFile)
	return nil
}

func unsetHelmEnv() error {
	os.Unsetenv(helmCacheHomeEnv)
	os.Unsetenv(helmCacheRepoEnv)
	os.Unsetenv(helmRepoEnv)
	return nil
}

var _ = Describe("Given manifest with oci specs", Ordered, func() {
	name := "valid-image-1"
	BeforeAll(func() {
		PushToRemoteOCIRegistry(name)
	})
	DescribeTable("Test ModuleStatus",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectManifestState func(manifestName string) error,
			expectedHelmClientCache func(cacheKey string) bool) {
			var manifest = NewTestManifest("manifest", "kyma")
			Eventually(givenCondition, Timeout, Interval).WithArguments(manifest).Should(Succeed())
			Eventually(expectManifestState, Timeout, Interval).WithArguments(manifest.GetName()).Should(Succeed())
			Eventually(expectedHelmClientCache, Timeout, Interval).WithArguments(manifest.GetLabels()[labels.CacheKey]).Should(BeTrue())
		},
		Entry("When manifestCR contains a valid OCI image specification, expect state in ready and helmClient cache exist",
			installWithValidImageSpec(name, false), expectManifestStateIn(v1alpha1.ManifestStateReady), expectHelmClientCacheExist(true)),
		Entry("When manifestCR contains an invalid OCI image specification, expect state in error and no helmClient cache exit",
			installWithInvalidImageSpec(false), expectManifestStateIn(v1alpha1.ManifestStateError), expectHelmClientCacheExist(false)),
	)
})

var _ = Describe("Test helm resources cleanup", Ordered, func() {
	name := "valid-image-2"
	BeforeAll(func() {
		PushToRemoteOCIRegistry(name)
	})
	It("should result in Kyma becoming Ready", func() {
		var manifest = NewTestManifest("manifest", "kyma")
		Eventually(installWithValidImageSpec(name, false), Timeout, Interval).WithArguments(manifest).Should(Succeed())
		validImageSpec := createImageSpec(name, server.Listener.Addr().String())
		deleteHelmChartResources(validImageSpec)
		verifyHelmResourcesDeletion(validImageSpec)
	})
})

var _ = Describe("Given manifest with kustomize, helm specs", func() {
	setHelmEnv()
	remoteKustomizeSpec := types.KustomizeSpec{
		URL:  "https://github.com/kyma-project/module-manager//operator/config/default?ref=main",
		Type: "kustomize",
	}
	remoteKustomizeSpecBytes, err := json.Marshal(remoteKustomizeSpec)
	Expect(err).ToNot(HaveOccurred())
	localKustomizeSpec := types.KustomizeSpec{
		Path: kustomizeLocalPath,
		Type: "kustomize",
	}
	localKustomizeSpecBytes, err := json.Marshal(localKustomizeSpec)
	Expect(err).ToNot(HaveOccurred())
	invalidKustomizeSpec := types.KustomizeSpec{
		Path: "./invalidPath",
		Type: "kustomize",
	}
	invalidKustomizeSpecBytes, err := json.Marshal(invalidKustomizeSpec)
	Expect(err).ToNot(HaveOccurred())

	validHelmChartSpec := types.HelmChartSpec{
		ChartName: "nginx-ingress",
		URL:       "https://helm.nginx.com/stable",
		Type:      "helm-chart",
	}
	validHelmChartSpecBytes, err := json.Marshal(validHelmChartSpec)
	Expect(err).ToNot(HaveOccurred())

	DescribeTable("Test ModuleStatus",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectedBehavior func(manifestName string) error) {
			var manifest = NewTestManifest("manifest", "kyma")
			Eventually(givenCondition, Timeout, Interval).WithArguments(manifest).Should(Succeed())
			Eventually(expectedBehavior, Timeout, Interval).WithArguments(manifest.GetName()).Should(Succeed())
		},
		Entry("When manifestCR contains a valid remote Kustomize specification, expect state in ready",
			addSpec(remoteKustomizeSpecBytes, false), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		Entry("When manifestCR contains a valid local Kustomize specification, expect state in ready",
			addSpec(localKustomizeSpecBytes, false), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		Entry("When manifestCR contains an invalid local Kustomize specification, expect state in error",
			addSpec(invalidKustomizeSpecBytes, false), expectManifestStateIn(v1alpha1.ManifestStateError)),
		Entry("When manifestCR contains a valid helm repo, expect state in ready",
			addSpec(validHelmChartSpecBytes, false), expectManifestStateIn(v1alpha1.ManifestStateReady)),
	)
})

func installWithInvalidImageSpec(remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		invalidImageSpec := createImageSpec("invalid-image-spec", "domain.invalid")
		imageSpecByte, err := json.Marshal(invalidImageSpec)
		Expect(err).ToNot(HaveOccurred())
		return installManifest(manifest, imageSpecByte, remote)
	}
}

func installWithValidImageSpec(name string, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		validImageSpec := createImageSpec(name, server.Listener.Addr().String())
		imageSpecByte, err := json.Marshal(validImageSpec)
		Expect(err).ToNot(HaveOccurred())
		return installManifest(manifest, imageSpecByte, remote)
	}
}

func installManifest(manifest *v1alpha1.Manifest, specByte []byte, remote bool) error {
	manifest.Spec.Remote = remote
	manifest.Spec.Installs = []v1alpha1.InstallInfo{
		{
			Source: runtime.RawExtension{
				Raw: specByte,
			},
			Name: "manifest-test",
		},
	}
	return k8sClient.Create(ctx, manifest)
}

func expectManifestStateIn(state v1alpha1.ManifestState) func(manifestName string) error {
	return func(manifestName string) error {
		manifestState := getManifestState(manifestName)
		if state != manifestState {
			return ErrManifestStateMisMatch
		}
		return nil
	}
}

func expectHelmClientCacheExist(expectExist bool) func(componentOwner string) bool {
	return func(componentOwner string) bool {
		key := client.ObjectKey{Name: componentOwner, Namespace: v1.NamespaceDefault}
		renderSrc := reconciler.CacheManager.GetRendererCache().GetProcessor(key)
		if expectExist {
			return renderSrc != nil
		}
		return renderSrc == nil
	}
}

func addSpec(specBytes []byte, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		return installManifest(manifest, specBytes, remote)
	}
}
