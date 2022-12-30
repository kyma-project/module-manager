package controllers_test

import (
	"encoding/json"
	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/pkg/labels"
	"github.com/kyma-project/module-manager/pkg/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Given manifest with OCI specs", Ordered, func() {
	BeforeAll(func() {
		installAuthSecret()
	})
	DescribeTable("Test OCI specs",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectManifestState func(manifestName string) error,
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
		Entry("When Manifest CR contains a valid install OCI image specification, "+
			"expect state in ready and helmClient cache exist",
			withRemoteInstallImageSpec("demo", false),
			expectManifestStateIn(v1alpha1.ManifestStateReady), expectHelmClientCacheExist(true)),
	)
})

func installAuthSecret() {
	secret := &corev1.Secret{}
	secretFile, err := os.ReadFile("../pkg/test_samples/auth_secret.yaml")
	Expect(err).ToNot(HaveOccurred())
	err = yaml.Unmarshal(secretFile, secret)
	err = k8sClient.Create(ctx, secret)
	Expect(err).ToNot(HaveOccurred())
	err = k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{})
	Eventually(err, standardTimeout, standardInterval).Should(Succeed())
}

func withRemoteInstallImageSpec(name string, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		validImageSpec := createOCIImageSpecWithDigest(name,
			"registry-1.docker.io/xinruan718",
			"sha256:5404c26a9859e246652989d2dcee3ab7604f31bc6ba579a27102a15756f290a6")
		installImageSpecByte, err := json.Marshal(validImageSpec)
		Expect(err).ToNot(HaveOccurred())
		manifest.Spec.AuthSecretSelector = metav1.LabelSelector{
			MatchLabels: authSecretLabel(),
		}
		return installManifest(manifest, installImageSpecByte, types.ImageSpec{}, remote)
	}
}

func authSecretLabel() map[string]string {
	return map[string]string{labels.OCIRegistryCred: "test-operator"}
}
