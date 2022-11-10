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
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

const (
	Timeout  = time.Second * 60
	Interval = time.Millisecond * 250
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

var _ = Describe("given manifest with install specs", func() {
	setHelmEnv()
	//remoteKustomizeSpec := types.KustomizeSpec{
	//	URL:  "https://github.com/kyma-project/module-manager//operator/config/default?ref=main",
	//	Type: "kustomize",
	//}
	//remoteKustomizeSpecBytes, err := json.Marshal(remoteKustomizeSpec)
	//Expect(err).ToNot(HaveOccurred())
	//localKustomizeSpec := types.KustomizeSpec{
	//	Path: "./test_samples/kustomize",
	//	Type: "kustomize",
	//}
	//localKustomizeSpecBytes, err := json.Marshal(localKustomizeSpec)
	//Expect(err).ToNot(HaveOccurred())
	//invalidKustomizeSpec := types.KustomizeSpec{
	//	Path: "./invalidPath",
	//	Type: "kustomize",
	//}
	//invalidKustomizeSpecBytes, err := json.Marshal(invalidKustomizeSpec)
	//Expect(err).ToNot(HaveOccurred())
	//
	//validHelmChartSpec := types.HelmChartSpec{
	//	ChartName: "nginx-ingress",
	//	URL:       "https://helm.nginx.com/stable",
	//	Type:      "helm-chart",
	//}
	//validHelmChartSpecBytes, err := json.Marshal(validHelmChartSpec)
	//Expect(err).ToNot(HaveOccurred())

	DescribeTable("Test ModuleStatus",
		func(givenCondition func(manifest *v1alpha1.Manifest) error, expectedBehavior func(manifestName string) error) {
			var manifest = NewTestManifest("manifest")
			Eventually(givenCondition, Timeout, Interval).WithArguments(manifest).Should(Succeed())
			Eventually(expectedBehavior, Timeout, Interval).WithArguments(manifest.GetName()).Should(Succeed())
		},
		//Entry("When manifestCR contains a valid remote Kustomize specification, expect state in ready",
		//	addSpec(remoteKustomizeSpecBytes), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		//Entry("When manifestCR contains a valid local Kustomize specification, expect state in ready",
		//	addSpec(localKustomizeSpecBytes), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		//Entry("When manifestCR contains an invalid local Kustomize specification, expect state in error",
		//	addSpec(invalidKustomizeSpecBytes), expectManifestStateIn(v1alpha1.ManifestStateError)),
		//Entry("When manifestCR contains a valid helm repo, expect state in ready",
		//	addSpec(validHelmChartSpecBytes), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		Entry("When manifestCR contains a valid OCI image specification, expect state in ready",
			addValidImageSpec(true), expectManifestStateIn(v1alpha1.ManifestStateReady)),
		//Entry("When manifestCR contains an invalid OCI image specification, expect state in error",
		//	addInvalidImageSpec(), expectManifestStateIn(v1alpha1.ManifestStateError)),
	)
})

func addInvalidImageSpec(remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		invalidImageSpec := types.ImageSpec{
			Name: layerNameRef,
			Repo: "invalid.com",
			Type: "oci-ref",
		}
		invalidImageSpec.Ref = GetImageSpecFromMockOCIRegistry()
		imageSpecByte, err := json.Marshal(invalidImageSpec)
		Expect(err).ToNot(HaveOccurred())
		return installManifest(manifest, imageSpecByte, remote)
	}
}

func addValidImageSpec(remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		validImageSpec := types.ImageSpec{
			Name: layerNameRef,
			Repo: server.Listener.Addr().String(),
			Type: "oci-ref",
		}
		validImageSpec.Ref = GetImageSpecFromMockOCIRegistry()
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
			return errors.New("ManifestState not match")
		}
		return nil
	}
}

func addSpec(specBytes []byte, remote bool) func(manifest *v1alpha1.Manifest) error {
	return func(manifest *v1alpha1.Manifest) error {
		return installManifest(manifest, specBytes, remote)
	}
}
