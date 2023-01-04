package controllers_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"

	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	errors2 "k8s.io/apimachinery/pkg/api/errors"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
)

var ErrManifestStateMisMatch = errors.New("ManifestState mismatch")

func setHelmEnv() {
	os.Setenv(helmCacheHomeEnv, helmCacheHome)
	os.Setenv(helmCacheRepoEnv, helmCacheRepo)
	os.Setenv(helmRepoEnv, helmRepoFile)
}

var _ = Describe(
	"Given manifest with kustomize specs", Ordered, func() {
		remoteKustomizeSpec := types.KustomizeSpec{
			URL:  "https://github.com/kyma-project/lifecycle-manager//config/default?ref=main",
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

		BeforeEach(
			func() {
				// reset file mode permission to 777
				Expect(os.Chmod(kustomizeLocalPath, fs.ModePerm)).To(Succeed())
			},
		)
		AfterEach(
			func() {
				Expect(os.Chmod(kustomizeLocalPath, fs.ModePerm)).To(Succeed())
				Expect(os.RemoveAll(filepath.Join(kustomizeLocalPath, util.ManifestDir))).To(Succeed())
			},
		)
		DescribeTable(
			"Testing Kustomize test entries",
			func(
				givenCondition func(manifest *v1alpha1.Manifest) error,
				expectedManifestState func(manifestName string) error, expectedFileState func() bool,
			) {
				manifest := NewTestManifest("kust")
				Eventually(givenCondition, standardTimeout, standardInterval).
					WithArguments(manifest).Should(Succeed())
				Eventually(expectedManifestState, standardTimeout, standardInterval).
					WithArguments(manifest.GetName()).Should(Succeed())
				Eventually(expectedFileState, standardTimeout, standardInterval).Should(BeTrue())
				Eventually(deleteManifestAndVerify(manifest), standardTimeout, standardInterval).Should(Succeed())
			},
			Entry(
				"When Manifest CR contains a valid remote Kustomize specification, expect state in ready",
				addInstallSpec(remoteKustomizeSpecBytes, false),
				expectManifestStateIn(declarative.StateReady), skipExpect(),
			),
			Entry(
				"When Manifest CR contains a valid local Kustomize specification, expect state in ready",
				addInstallSpec(localKustomizeSpecBytes, false),
				expectManifestStateIn(declarative.StateReady), skipExpect(),
			),
			Entry(
				"When Manifest CR contains an invalid local Kustomize specification, expect state in error",
				addInstallSpec(invalidKustomizeSpecBytes, false),
				expectManifestStateIn(declarative.StateError), skipExpect(),
			),
			Entry(
				"When local Kustomize with read rights only, expect state in ready",
				addInstallSpecWithFilePermission(localKustomizeSpecBytes, false, 0o444),
				expectManifestStateIn(declarative.StateReady), skipExpect(),
			),
			Entry(
				"When local Kustomize with execute rights only, expect state in ready and file not exit",
				addInstallSpecWithFilePermission(localKustomizeSpecBytes, false, 0o555),
				expectManifestStateIn(declarative.StateReady), expectFileNotExistError(),
			),
		)
	},
)

var _ = Describe(
	"Given manifest with OCI specs", Ordered, func() {
		mainOciTempDir := "main-dir"
		installName := filepath.Join(mainOciTempDir, "installs")
		crdName := filepath.Join(mainOciTempDir, "crds")
		BeforeAll(
			func() {
				PushToRemoteOCIRegistry(installName, layerInstalls)
				PushToRemoteOCIRegistry(crdName, layerCRDs)
			},
		)
		BeforeEach(
			func() {
				Expect(os.RemoveAll(filepath.Join(os.TempDir(), mainOciTempDir))).To(Succeed())
			},
		)
		DescribeTable(
			"Test OCI specs",
			func(
				givenCondition func(manifest *v1alpha1.Manifest) error,
				expectManifestState func(manifestName string) error,
				expectedHelmClientCache func(cacheKey string) bool,
			) {
				manifest := NewTestManifest("oci")
				Eventually(givenCondition, standardTimeout, standardInterval).
					WithArguments(manifest).Should(Succeed())
				Eventually(expectManifestState, standardTimeout, standardInterval).
					WithArguments(manifest.GetName()).Should(Succeed())
				Eventually(expectedHelmClientCache, standardTimeout, standardInterval).
					WithArguments(manifest.GetLabels()[labels.ComponentOwner]).Should(BeTrue())
				Eventually(deleteManifestAndVerify(manifest), standardTimeout, standardInterval).Should(Succeed())
			},
			Entry(
				"When Manifest CR contains a valid install OCI image specification, "+
					"expect state in ready and helmClient cache exist",
				withValidInstallImageSpec(installName, false),
				expectManifestStateIn(declarative.StateReady), expectHelmClientCacheExist(true),
			),
			Entry(
				"When Manifest CR contains a valid install OCI image specification and enabled remote, "+
					"expect state in ready and helmClient cache exist",
				withValidInstallImageSpec(installName, true),
				expectManifestStateIn(declarative.StateReady), expectHelmClientCacheExist(true),
			),
			Entry(
				"When Manifest CR contains valid install and CRD image specification, "+
					"expect state in ready and helmClient cache exist",
				withValidInstallAndCRDsImageSpec(installName, crdName, true),
				expectManifestStateIn(declarative.StateReady), expectHelmClientCacheExist(true),
			),
			Entry(
				"When Manifest CR contains an invalid install OCI image specification, "+
					"expect state in error and no helmClient cache exit",
				withInvalidInstallImageSpec(false),
				expectManifestStateIn(declarative.StateError), expectHelmClientCacheExist(false),
			),
		)
	},
)

var _ = Describe(
	"Given Manifest CR with Helm specs", func() {
		setHelmEnv()
		validHelmChartSpec := types.HelmChartSpec{
			ChartName: "nginx-ingress",
			URL:       "https://helm.nginx.com/stable",
			Type:      "helm-chart",
		}
		validHelmChartSpecBytes, err := json.Marshal(validHelmChartSpec)
		Expect(err).ToNot(HaveOccurred())

		DescribeTable(
			"Test Helm specs",
			func(
				givenCondition func(manifest *v1alpha1.Manifest) error,
				expectedBehavior func(manifestName string) error,
			) {
				manifest := NewTestManifest("helm")
				Eventually(givenCondition, standardTimeout, standardInterval).WithArguments(manifest).Should(Succeed())
				Eventually(
					expectedBehavior, standardTimeout, standardInterval,
				).WithArguments(manifest.GetName()).Should(Succeed())
				Eventually(deleteManifestAndVerify(manifest), standardTimeout, standardInterval).Should(Succeed())
			},
			Entry(
				"When manifestCR contains a valid helm repo, expect state in ready",
				addInstallSpec(validHelmChartSpecBytes, false), expectManifestStateIn(declarative.StateReady),
			),
		)
	},
)

var _ = Describe(
	"Test multiple Manifest CRs with same parent and OCI spec", Ordered, func() {
		mainOciTempDir := "multiple"
		installName := filepath.Join(mainOciTempDir, "crs")
		BeforeAll(
			func() {
				PushToRemoteOCIRegistry(installName, layerInstalls)
			},
		)
		BeforeEach(
			func() {
				Expect(os.RemoveAll(filepath.Join(os.TempDir(), mainOciTempDir))).To(Succeed())
			},
		)
		It(
			"should result in Manifest becoming Ready", func() {
				manifestWithInstall := NewTestManifest("multi-oci1")
				Eventually(withValidInstallImageSpec(installName, false), standardTimeout, standardInterval).
					WithArguments(manifestWithInstall).Should(Succeed())
				validImageSpec := createImageSpec(installName, server.Listener.Addr().String(), layerInstalls)
				Eventually(expectHelmClientCacheExist(true), standardTimeout, standardInterval).
					WithArguments(manifestWithInstall.GetLabels()[labels.ComponentOwner]).Should(BeTrue())
				// this will ensure only manifest.yaml remains
				deleteHelmChartResources(validImageSpec)
				manifest2WithInstall := NewTestManifest("multi-oci2")
				// copy owner label over to the new manifest resource
				manifest2WithInstall.Labels[labels.ComponentOwner] = manifestWithInstall.Labels[labels.ComponentOwner]
				Eventually(withValidInstallImageSpec(installName, false), standardTimeout, standardInterval).
					WithArguments(manifest2WithInstall).Should(Succeed())
				// verify no new Helm resources were created
				verifyHelmResourcesDeletion(validImageSpec)
				// fresh Manifest with empty installs
				manifest3WithoutInstall := NewTestManifest("multi-oci3")
				Eventually(withEmptyInstallImageSpec(), standardTimeout, standardInterval).
					WithArguments(manifest3WithoutInstall).Should(Succeed())
				// no cache entry created for empty installs
				Eventually(expectHelmClientCacheExist(false), standardTimeout, standardInterval).
					WithArguments(manifest3WithoutInstall.GetLabels()[labels.ComponentOwner]).Should(BeTrue())
				Eventually(
					deleteManifestAndVerify(manifestWithInstall), standardTimeout, standardInterval,
				).Should(Succeed())
				Eventually(
					deleteManifestAndVerify(manifest2WithInstall), standardTimeout, standardInterval,
				).Should(Succeed())
				Eventually(
					deleteManifestAndVerify(manifest3WithoutInstall), standardTimeout, standardInterval,
				).Should(Succeed())
			},
		)
	},
)

func skipExpect() func() bool {
	return func() bool {
		return true
	}
}

func expectHelmClientCacheExist(expectExist bool) func(componentOwner string) bool {
	return func(componentOwner string) bool {
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

func withEmptyInstallImageSpec() func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		return installManifest(manifest, nil, types.ImageSpec{}, false)
	}
}

func withValidInstallAndCRDsImageSpec(
	installName,
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

func expectManifestStateIn(state declarative.State) func(manifestName string) error {
	return func(manifestName string) error {
		status, err := getManifestStatus(manifestName)
		if err != nil {
			return err
		}
		if state != status.State {
			return fmt.Errorf("status is %v but expected %s: %w", status, state, ErrManifestStateMisMatch)
		}
		return nil
	}
}

func getManifestStatus(manifestName string) (declarative.Status, error) {
	manifest := &v1alpha1.Manifest{}

	err := k8sClient.Get(
		ctx, client.ObjectKey{
			Namespace: v1.NamespaceDefault,
			Name:      manifestName,
		}, manifest,
	)
	if err != nil {
		return declarative.Status{}, err
	}
	return declarative.Status(manifest.Status), nil
}

func deleteManifestAndVerify(manifest *v1alpha1.Manifest) func() error {
	return func() error {
		// reverting permissions for deletion - in case it was changed during tests
		if err := os.Chmod(kustomizeLocalPath, fs.ModePerm); err != nil {
			return err
		}
		if err := k8sClient.Delete(ctx, manifest); err != nil && !errors2.IsNotFound(err) {
			return err
		}
		newManifest := v1alpha1.Manifest{}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(manifest), &newManifest)
		if errors2.IsNotFound(err) {
			return nil
		}
		return err
	}
}

func addInstallSpec(specBytes []byte, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		return installManifest(manifest, specBytes, types.ImageSpec{}, remote)
	}
}

func addInstallSpecWithFilePermission(
	specBytes []byte,
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

func expectFileNotExistError() func() bool {
	return func() bool {
		_, err := os.Stat(filepath.Join(kustomizeLocalPath, util.ManifestDir))
		return os.IsNotExist(err)
	}
}
