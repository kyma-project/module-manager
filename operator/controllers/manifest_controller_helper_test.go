package controllers_test

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	_ "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

type mockLayer struct {
	filePath string
}

func (m mockLayer) Digest() (v1.Hash, error) {
	r, err := m.Compressed()
	if err != nil {
		return v1.Hash{}, err
	}
	defer r.Close()
	hash, _, err := v1.SHA256(r)
	return hash, err
}

func (m mockLayer) MediaType() (types.MediaType, error) {
	return types.OCILayer, nil
}

func (m mockLayer) Size() (int64, error) { return 137438691328, nil }
func (m mockLayer) Compressed() (io.ReadCloser, error) {
	f, err := os.Open(m.filePath)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(f), nil
}

func (m mockLayer) Uncompressed() (io.ReadCloser, error) {
	f, err := os.Open("../pkg/test_samples/oci/config.yaml")
	if err != nil {
		return nil, err
	}
	return io.NopCloser(f), nil
}

func (m mockLayer) DiffID() (v1.Hash, error) {
	return v1.Hash{Algorithm: "fake", Hex: "diff id"}, nil
}

func GetImageSpecFromMockOCIRegistry() (manifestTypes.ImageSpec, manifestTypes.ImageSpec) {
	// create registry and server

	// install layer
	layerNameRef := filepath.Join(layerNameBaseDir, layerNameSubDir)
	installLayer, err := partial.CompressedToLayer(mockLayer{filePath: "../pkg/test_samples/oci/compressed.tgz"})
	Expect(err).ToNot(HaveOccurred())
	installDigest, err := installLayer.Digest()
	Expect(err).ToNot(HaveOccurred())

	// crd layer
	crdLayer, err := partial.CompressedToLayer(mockLayer{filePath: "../pkg/test_samples/oci/crd.tgz"})
	Expect(err).ToNot(HaveOccurred())
	crdDigest, err := installLayer.Digest()
	Expect(err).ToNot(HaveOccurred())

	// Set up a fake registry and write what we pulled to it.
	u, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())

	installDst := fmt.Sprintf("%s/%s@%s", u.Host, layerNameRef, installDigest)
	installRef, err := name.NewDigest(installDst)
	Expect(err).ToNot(HaveOccurred())

	crdDst := fmt.Sprintf("%s/%s@%s", u.Host, layerNameRef, crdDigest)
	crdRef, err := name.NewDigest(crdDst)
	Expect(err).ToNot(HaveOccurred())

	err = remote.WriteLayer(installRef.Context(), installLayer)
	Expect(err).ToNot(HaveOccurred())
	err = remote.WriteLayer(crdRef.Context(), crdLayer)
	Expect(err).ToNot(HaveOccurred())

	gotInstall, err := remote.Layer(installRef)
	Expect(err).ToNot(HaveOccurred())
	gotInstallHash, err := gotInstall.Digest()
	Expect(err).ToNot(HaveOccurred())
	Expect(gotInstallHash).To(Equal(installDigest))

	gotCrd, err := remote.Layer(crdRef)
	Expect(err).ToNot(HaveOccurred())
	gotCrdHash, err := gotCrd.Digest()
	Expect(err).ToNot(HaveOccurred())
	Expect(gotCrdHash).To(Equal(crdDigest))

	installHash, err := installLayer.Digest()
	Expect(err).ToNot(HaveOccurred())

	crdHash, err := crdLayer.Digest()
	Expect(err).ToNot(HaveOccurred())

	return getImageSpec(crdHash.String(), layerNameRef), getImageSpec(installHash.String(), layerNameRef)
}

func getImageSpec(digest string, layerNameRef string) manifestTypes.ImageSpec {
	return manifestTypes.ImageSpec{
		Name: layerNameRef,
		Repo: server.Listener.Addr().String(),
		Ref:  digest,
		Type: "oci-ref",
	}
}

func createKymaSecret() *corev1.Secret {
	kymaSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: corev1.NamespaceDefault,
		},
		StringData: map[string]string{},
	}
	Expect(k8sClient.Create(ctx, kymaSecret)).Should(Succeed())
	return kymaSecret
}

func createManifestObj(name string, spec v1alpha1.ManifestSpec) *v1alpha1.Manifest {
	return &v1alpha1.Manifest{
		TypeMeta: metav1.TypeMeta{
			Kind: "Manifest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				labels.ComponentOwner: secretName,
			},
		},
		Spec: spec,
	}
}

func deleteHelmChartResources(imageSpec manifestTypes.ImageSpec) {
	chartYamlPath := filepath.Join(util.GetFsChartPath(imageSpec), "Chart.yaml")
	Expect(os.RemoveAll(chartYamlPath)).Should(Succeed())
	valuesYamlPath := filepath.Join(util.GetFsChartPath(imageSpec), "values.yaml")
	Expect(os.RemoveAll(valuesYamlPath)).Should(Succeed())
	templatesPath := filepath.Join(util.GetFsChartPath(imageSpec), "templates")
	Expect(os.RemoveAll(templatesPath)).Should(Succeed())
}

func verifyHelmResourcesDeletion(imageSpec manifestTypes.ImageSpec) {
	_, err := os.Stat(filepath.Join(util.GetFsChartPath(imageSpec), "Chart.yaml"))
	Expect(os.IsNotExist(err)).To(BeTrue())
	_, err = os.Stat(filepath.Join(util.GetFsChartPath(imageSpec), "values.yaml"))
	Expect(os.IsNotExist(err)).To(BeTrue())
	_, err = os.Stat(filepath.Join(util.GetFsChartPath(imageSpec), "templates"))
	Expect(os.IsNotExist(err)).To(BeTrue())
}

func deleteManifestResource(manifestObj *v1alpha1.Manifest, secret *corev1.Secret) {
	Expect(k8sClient.Delete(ctx, manifestObj)).Should(Succeed())
	Eventually(getManifest(client.ObjectKeyFromObject(manifestObj)), standardTimeout, standardInterval).
		Should(BeTrue())
	if secret != nil {
		Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
	}
}
