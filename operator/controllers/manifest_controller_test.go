package controllers_test

import (
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

func createManifest() *v1alpha1.Manifest {
	helmChartSpec := types.HelmChartSpec{
		ChartName: "nginx-ingress",
		URL:       "https://helm.nginx.com/stable",
		Type:      "helm-chart",
	}
	specBytes, err := json.Marshal(helmChartSpec)
	Expect(err).ToNot(HaveOccurred())
	return &v1alpha1.Manifest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       v1alpha1.ManifestKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manifest-sample",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: v1alpha1.ManifestSpec{
			Installs: []v1alpha1.InstallInfo{
				{
					Source: runtime.RawExtension{
						Raw: specBytes,
					},
					Name: "nginx-stable",
				},
			},
		},
	}
}

func getManifestState(key client.ObjectKey) func() string {
	return func() string {
		manifest := v1alpha1.Manifest{}
		err := k8sClient.Get(ctx, key, &manifest)
		if err != nil {
			return ""
		}
		return string(manifest.Status.State)
	}
}

func createRepo() error {
	err := os.MkdirAll(helmCacheHome, os.ModePerm)
	if err != nil {
		return err
	}
	os.Setenv(helmCacheHomeEnv, helmCacheHome)
	os.Setenv(helmCacheRepoEnv, helmCacheHomeEnv)
	return nil
}

func deleteRepo() error {
	err := os.RemoveAll(helmCacheHome)
	if err != nil {
		return err
	}
	os.Unsetenv(helmCacheHomeEnv)
	os.Unsetenv(helmCacheRepoEnv)
	return nil
}

var _ = Describe("given manifest with", Ordered, func() {
	manifest := createManifest()
	BeforeAll(func() {
		createRepo()
	})
	BeforeEach(func() {
		Expect(k8sClient.Create(ctx, manifest)).Should(Succeed())
	})
	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, manifest)).Should(Succeed())
	})

	It("Should result in a ready state", func() {
		By("having transitioned the CR State to Ready")
		Eventually(getManifestState(client.ObjectKeyFromObject(manifest)), 5*time.Minute, 250*time.Millisecond).
			Should(BeEquivalentTo(string(v1alpha1.ManifestStateReady)))
	})

	AfterAll(func() {
		deleteRepo()
	})
})
