package controllers_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var ErrManifestStateMisMatch = errors.New("ManifestState mismatch")

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

func setHelmEnv() {
	os.Setenv(helmCacheHomeEnv, helmCacheHome)
	os.Setenv(helmCacheRepoEnv, helmCacheRepo)
	os.Setenv(helmRepoEnv, helmRepoFile)
}

var _ = Describe("Given manifest with kustomize specs", Ordered, func() {
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

	BeforeEach(func() {
		// reset file mode permission to 777
		Expect(os.Chmod(kustomizeLocalPath, fs.ModePerm)).To(Succeed())
	})
	AfterEach(func() {
		Expect(os.Chmod(kustomizeLocalPath, fs.ModePerm)).To(Succeed())
		Expect(os.RemoveAll(filepath.Join(kustomizeLocalPath, util.ManifestDir))).To(Succeed())
	})
	DescribeTable("Test file permission",
		func(givenCondition func(manifest *v1alpha1.Manifest) error,
			expectedManifestState func(manifestName string) error, expectedFileState func() bool,
		) {
			manifest := NewTestManifest("manifest", "kyma")

			Eventually(givenCondition, standardTimeout, standardInterval).
				WithArguments(manifest).Should(Succeed())
			Eventually(expectedManifestState, standardTimeout, standardInterval).
				WithArguments(manifest.GetName()).Should(Succeed())
			Eventually(expectedFileState, standardTimeout, standardInterval).Should(BeTrue())
		},
		Entry("When manifestCR contains a valid remote Kustomize specification, expect state in ready",
			addInstallSpec(remoteKustomizeSpecBytes, false),
			expectManifestStateIn(v1alpha1.ManifestStateReady), skipExpect()),
		Entry("When manifestCR contains a valid local Kustomize specification, expect state in ready",
			addInstallSpec(localKustomizeSpecBytes, false),
			expectManifestStateIn(v1alpha1.ManifestStateReady), skipExpect()),
		Entry("When manifestCR contains an invalid local Kustomize specification, expect state in error",
			addInstallSpec(invalidKustomizeSpecBytes, false),
			expectManifestStateIn(v1alpha1.ManifestStateError), skipExpect()),
		Entry("When local Kustomize with read rights only, expect state in error and file permission denied",
			addInstallSpecWithFilePermission(localKustomizeSpecBytes, false, 0o444),
			expectManifestStateIn(v1alpha1.ManifestStateError), expectFilePermissionDeniedError()),
		Entry("When local kustomize with execute rights only, expect state in ready and file not exit",
			addInstallSpecWithFilePermission(localKustomizeSpecBytes, false, 0o555),
			expectManifestStateIn(v1alpha1.ManifestStateReady), expectFileNotExistError()),
	)
})

var _ = Describe("Given manifest with oci specs", Ordered, func() {
	installName := "valid-install-layer"
	crdName := "valid-crd-layer"
	BeforeAll(func() {
		PushToRemoteOCIRegistry(installName, layerInstalls)
		PushToRemoteOCIRegistry(crdName, layerCRDs)
	})
	BeforeEach(func() {
		fschartPath := util.GetFsChartPath(
			createImageSpec(installName, server.Listener.Addr().String(), layerInstalls))
		manifestPath := util.GetFsManifestChartPath(fschartPath)
		_ = os.Remove(manifestPath)
	})
	DescribeTable("Test ModuleStatus",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectManifestState func(manifestName string) error,
			expectedHelmClientCache func(cacheKey string) bool,
		) {
			manifest := NewTestManifest("manifest", "kyma")
			Eventually(givenCondition, standardTimeout, standardInterval).
				WithArguments(manifest).Should(Succeed())
			Eventually(expectManifestState, standardTimeout, standardInterval).
				WithArguments(manifest.GetName()).Should(Succeed())
			Eventually(expectedHelmClientCache, standardTimeout, standardInterval).
				WithArguments(manifest.GetLabels()[labels.CacheKey]).Should(BeTrue())
		},
		Entry("When manifestCR contains a valid install OCI image specification, "+
			"expect state in ready and helmClient cache exist",
			withValidInstallImageSpec(installName, false),
			expectManifestStateIn(v1alpha1.ManifestStateReady), expectHelmClientCacheExist(true),
		),
		Entry("When manifestCR contains a valid install OCI image specification and enabled remote, "+
			"expect state in ready and helmClient cache exist",
			withValidInstallImageSpec(installName, true),
			expectManifestStateIn(v1alpha1.ManifestStateReady), expectHelmClientCacheExist(true),
		),
		Entry("When manifestCR contains valid install and CRD image specification, "+
			"expect state in ready and helmClient cache exist",
			withValidInstallAndCRDsImageSpec(installName, crdName, true),
			expectManifestStateIn(v1alpha1.ManifestStateReady), expectHelmClientCacheExist(true),
		),
		Entry("When manifestCR contains an invalid install OCI image specification, "+
			"expect state in error and no helmClient cache exit",
			withInvalidInstallImageSpec(false),
			expectManifestStateIn(v1alpha1.ManifestStateError), expectHelmClientCacheExist(false),
		),
	)
})

var _ = Describe("Given manifest with helm specs", func() {
	setHelmEnv()
	validHelmChartSpec := types.HelmChartSpec{
		ChartName: "nginx-ingress",
		URL:       "https://helm.nginx.com/stable",
		Type:      "helm-chart",
	}
	validHelmChartSpecBytes, err := json.Marshal(validHelmChartSpec)
	Expect(err).ToNot(HaveOccurred())

	DescribeTable("Test ModuleStatus",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectedBehavior func(manifestName string) error) {
			manifest := NewTestManifest("manifest", "kyma")
			Eventually(givenCondition, standardTimeout, standardInterval).WithArguments(manifest).Should(Succeed())
			Eventually(expectedBehavior, standardTimeout, standardInterval).WithArguments(manifest.GetName()).Should(Succeed())
		},
		Entry("When manifestCR contains a valid helm repo, expect state in ready",
			addInstallSpec(validHelmChartSpecBytes, false), expectManifestStateIn(v1alpha1.ManifestStateReady)),
	)
})

var _ = Describe("Test helm resources cleanup", Ordered, func() {
	name := "valid-image-2"
	BeforeAll(func() {
		PushToRemoteOCIRegistry(name, layerInstalls)
	})
	It("should result in Kyma becoming Ready", func() {
		manifest := NewTestManifest("manifest", "kyma")
		Eventually(withValidInstallImageSpec(name, false), standardTimeout, standardInterval).
			WithArguments(manifest).Should(Succeed())
		validImageSpec := createImageSpec(name, server.Listener.Addr().String(), layerInstalls)
		deleteHelmChartResources(validImageSpec)
		verifyHelmResourcesDeletion(validImageSpec)
	})
})

func skipExpect() func() bool {
	return func() bool {
		return true
	}
}

func withInvalidInstallImageSpec(remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		invalidImageSpec := createImageSpec("invalid-image-spec", "domain.invalid", layerInstalls)
		imageSpecByte, err := json.Marshal(invalidImageSpec)
		Expect(err).ToNot(HaveOccurred())
		return installManifest(manifest, imageSpecByte, types.ImageSpec{}, remote)
	}
}

func withValidInstallImageSpec(name string, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		validImageSpec := createImageSpec(name, server.Listener.Addr().String(), layerInstalls)
		imageSpecByte, err := json.Marshal(validImageSpec)
		Expect(err).ToNot(HaveOccurred())
		return installManifest(manifest, imageSpecByte, types.ImageSpec{}, remote)
	}
}

func withValidInstallAndCRDsImageSpec(installName,
	crdName string, remote bool,
) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		validInstallImageSpec := createImageSpec(installName, server.Listener.Addr().String(), layerInstalls)
		installSpecByte, err := json.Marshal(validInstallImageSpec)
		Expect(err).ToNot(HaveOccurred())

		validCRDsImageSpec := createImageSpec(crdName, server.Listener.Addr().String(), layerCRDs)
		return installManifest(manifest, installSpecByte, validCRDsImageSpec, remote)
	}
}

func installManifest(manifest *v1alpha1.Manifest, installSpecByte []byte, crdSpec types.ImageSpec, remote bool) error {
	if installSpecByte != nil {
		manifest.Spec.Installs = []v1alpha1.InstallInfo{
			{
				Source: runtime.RawExtension{
					Raw: installSpecByte,
				},
				Name: "manifest-test",
			},
		}
	} else {
		manifest.Spec.Installs = make([]v1alpha1.InstallInfo, 0)
	}
	manifest.Spec.CRDs = crdSpec
	if remote {
		manifest.Spec.Remote = true
		manifest.Spec.Resource = unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "operator.kyma-project.io/v1alpha1",
				"kind":       "SampleCRD",
				"metadata": map[string]interface{}{
					"name":      "sample-crd-from-manifest",
					"namespace": v1.NamespaceDefault,
				},
				"namespace": "default",
			},
		}
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

func addInstallSpec(specBytes []byte, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		return installManifest(manifest, specBytes, types.ImageSpec{}, remote)
	}
}

func addInstallSpecWithFilePermission(specBytes []byte,
	remote bool, fileMode os.FileMode,
) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		user, err := user.Current()
		Expect(err).ToNot(HaveOccurred())
		if user.Username == "root" {
			Skip("This test is not suitable for user with root privileges")
		}
		// should not be run as root user
		Expect(user.Username).ToNot(Equal("root"))
		Expect(os.Chmod(kustomizeLocalPath, fileMode)).ToNot(HaveOccurred())
		return installManifest(manifest, specBytes, types.ImageSpec{}, remote)
	}
}

func expectFilePermissionDeniedError() func() bool {
	return func() bool {
		_, err := os.Stat(filepath.Join(kustomizeLocalPath, util.ManifestDir))
		return os.IsPermission(err)
	}
}

func expectFileNotExistError() func() bool {
	return func() bool {
		_, err := os.Stat(filepath.Join(kustomizeLocalPath, util.ManifestDir))
		return os.IsNotExist(err)
	}
}
