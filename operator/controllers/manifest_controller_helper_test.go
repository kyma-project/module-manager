package controllers_test

import (
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/pkg/labels"
	manifestTypes "github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
	_ "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func CreateImageSpecLayer() v1.Layer {
	// create registry and server
	layer, err := partial.CompressedToLayer(mockLayer{})
	Expect(err).ToNot(HaveOccurred())
	return layer
}

func PushToRemoteOCIRegistry(layerName string) {
	layer := CreateImageSpecLayer()
	digest, err := layer.Digest()
	Expect(err).ToNot(HaveOccurred())

	// Set up a fake registry and write what we pulled to it.
	u, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())

	dst := fmt.Sprintf("%s/%s@%s", u.Host, layerName, digest)
	ref, err := name.NewDigest(dst)
	Expect(err).ToNot(HaveOccurred())

	err = remote.WriteLayer(ref.Context(), layer)
	Expect(err).ToNot(HaveOccurred())

	got, err := remote.Layer(ref)
	Expect(err).ToNot(HaveOccurred())
	gotHash, err := got.Digest()
	Expect(err).ToNot(HaveOccurred())
	Expect(gotHash).To(Equal(digest))
}

func createImageSpec(name, repo string) manifestTypes.ImageSpec {
	imageSpec := manifestTypes.ImageSpec{
		Name: name,
		Repo: repo,
		Type: "oci-ref",
	}
	layer := CreateImageSpecLayer()
	digest, err := layer.Digest()
	Expect(err).ToNot(HaveOccurred())
	imageSpec.Ref = digest.String()
	return imageSpec
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
	parsedURL, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())

	installDst := fmt.Sprintf("%s/%s@%s", parsedURL.Host, layerNameRef, installDigest)
	installRef, err := name.NewDigest(installDst)
	Expect(err).ToNot(HaveOccurred())

	crdDst := fmt.Sprintf("%s/%s@%s", parsedURL.Host, layerNameRef, crdDigest)
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

func createKymaSecret(name string) *corev1.Secret {
	kymaSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: corev1.NamespaceDefault,
		},
		StringData: map[string]string{},
	}
	Expect(k8sClient.Create(ctx, kymaSecret)).Should(Succeed())
	return kymaSecret
}

func NewTestManifest(name string, componentOwner string) *v1alpha1.Manifest {
	return &v1alpha1.Manifest{
		TypeMeta: metav1.TypeMeta{
			Kind: "Manifest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + RandString(8),
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				labels.ComponentOwner: componentOwner,
				labels.CacheKey:       RandString(8),
			},
		},
	}
}

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))] //nolint:gosec
	}
	return string(b)
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
