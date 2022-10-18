package controllers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

func createManifestWithHelmRepo() func() bool {
	return func() bool {
		By("having transitioned the CR State to Ready")
		helmChartSpec := types.HelmChartSpec{
			ChartName: "nginx-ingress",
			URL:       "https://helm.nginx.com/stable",
			Type:      "helm-chart",
		}
		specBytes, err := json.Marshal(helmChartSpec)
		Expect(err).ToNot(HaveOccurred())
		manifestObj := createManifestObj("manifest-sample", v1alpha1.ManifestSpec{
			Installs: []v1alpha1.InstallInfo{
				{
					Source: runtime.RawExtension{
						Raw: specBytes,
					},
					Name: "nginx-stable",
				},
			},
		})
		Expect(k8sClient.Create(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifestState(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeEquivalentTo(v1alpha1.ManifestStateReady))
		Expect(k8sClient.Delete(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifest(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeTrue())
		return true
	}
}

func createManifestWithOCI() func() bool {
	return func() bool {
		By("having transitioned the CR State to Ready")
		digest := CreateFakeOCIRegistry()
		imageSpec := types.ImageSpec{
			Name: "some/name",
			Repo: server.Listener.Addr().String(),
			Ref:  digest,
			Type: "oci-ref",
		}
		specBytes, err := json.Marshal(imageSpec)
		Expect(err).ToNot(HaveOccurred())
		manifestObj := createManifestObj("manifest-sample", v1alpha1.ManifestSpec{
			Installs: []v1alpha1.InstallInfo{
				{
					Source: runtime.RawExtension{
						Raw: specBytes,
					},
					Name: "oci-image",
				},
			},
		})
		Expect(k8sClient.Create(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifestState(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeEquivalentTo(v1alpha1.ManifestStateReady))
		Expect(k8sClient.Delete(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifest(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeTrue())

		// delete Chart.yaml and values.yaml from rendered helm chart
		// to check if only fs cached rendered manifest is used to reconcile manifest
		chartYamlPath := filepath.Join(util.GetFsChartPath(imageSpec), "Chart.yaml")
		valuesYamlPath := filepath.Join(util.GetFsChartPath(imageSpec), "values.yaml")
		Expect(os.RemoveAll(chartYamlPath)).Should(Succeed())
		Expect(os.RemoveAll(valuesYamlPath)).Should(Succeed())

		// create another manifest with same image specification
		manifestObj2 := createManifestObj("manifest-sample-2", v1alpha1.ManifestSpec{
			Installs: []v1alpha1.InstallInfo{
				{
					Source: runtime.RawExtension{
						Raw: specBytes,
					},
					Name: "oci-image",
				},
			},
		})

		Expect(k8sClient.Create(ctx, manifestObj2)).Should(Succeed())
		Eventually(getManifestState(client.ObjectKeyFromObject(manifestObj2)), 5*time.Minute, 250*time.Millisecond).
			Should(BeEquivalentTo(v1alpha1.ManifestStateReady))

		// verify deletes files were still not created
		_, err = os.Stat(chartYamlPath)
		Expect(os.IsNotExist(err)).To(BeTrue())
		_, err = os.Stat(valuesYamlPath)
		Expect(os.IsNotExist(err)).To(BeTrue())

		Expect(k8sClient.Delete(ctx, manifestObj2)).Should(Succeed())
		Eventually(getManifest(client.ObjectKeyFromObject(manifestObj2)), 5*time.Minute, 250*time.Millisecond).
			Should(BeTrue())
		Expect(os.RemoveAll(util.GetFsChartPath(imageSpec))).Should(Succeed())
		return true
	}
}

func createManifestWithInvalidOCI() func() bool {
	return func() bool {
		By("having transitioned the CR State to Ready")
		digest := CreateFakeOCIRegistry()
		imageSpec := types.ImageSpec{
			Name: "some/name",
			Repo: "invalid.com",
			Ref:  digest,
			Type: "oci-ref",
		}
		specBytes, err := json.Marshal(imageSpec)
		Expect(err).ToNot(HaveOccurred())
		manifestObj := createManifestObj("manifest-sample", v1alpha1.ManifestSpec{
			Installs: []v1alpha1.InstallInfo{
				{
					Source: runtime.RawExtension{
						Raw: specBytes,
					},
					Name: "oci-image",
				},
			},
		})
		Expect(k8sClient.Create(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifestState(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeEquivalentTo(v1alpha1.ManifestStateError))
		Expect(k8sClient.Delete(ctx, manifestObj)).Should(Succeed())
		Eventually(getManifest(client.ObjectKeyFromObject(manifestObj)), 5*time.Minute, 250*time.Millisecond).
			Should(BeTrue())
		Expect(os.RemoveAll(util.GetFsChartPath(imageSpec))).Should(Succeed())
		return true
	}
}

func getManifestState(key client.ObjectKey) func() v1alpha1.ManifestState {
	return func() v1alpha1.ManifestState {
		manifest := v1alpha1.Manifest{}
		err := k8sClient.Get(ctx, key, &manifest)
		if err != nil {
			return ""
		}
		return manifest.Status.State
	}
}

func getManifest(key client.ObjectKey) func() bool {
	return func() bool {
		manifest := v1alpha1.Manifest{}
		err := k8sClient.Get(ctx, key, &manifest)
		return errors.IsNotFound(err)
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

var _ = Describe("given manifest with a helm repo", Ordered, func() {
	BeforeAll(func() {
		Expect(setHelmEnv()).Should(Succeed())
	})

	DescribeTable("given watcherCR reconcile loop",
		func(testCaseFn func() bool) {
			Expect(testCaseFn()).To(BeTrue())
		},
		[]TableEntry{
			Entry("when manifestCR contains a valid helm repo", createManifestWithHelmRepo()),
			Entry("when manifestCRs contain valid OCI Image specification", createManifestWithOCI()),
			Entry("when manifestCR contains invalid OCI Image specification", createManifestWithInvalidOCI()),
		})

	AfterAll(func() {
		Expect(unsetHelmEnv()).Should(Succeed())
	})
})
