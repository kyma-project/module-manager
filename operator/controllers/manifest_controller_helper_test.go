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

type mockLayer struct{}

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
	f, err := os.Open("./test_samples/helm/compressed.tgz")
	if err != nil {
		return nil, err
	}
	return io.NopCloser(f), nil
}

func GetImageSpecFromMockOCIRegistry() manifestTypes.ImageSpec {
	// create registry and server
	layer, err := partial.CompressedToLayer(mockLayer{})
	Expect(err).ToNot(HaveOccurred())
	digest, err := layer.Digest()
	Expect(err).ToNot(HaveOccurred())

	// Set up a fake registry and write what we pulled to it.
	u, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())

	dst := fmt.Sprintf("%s/%s@%s", u.Host, layerNameRef, digest)
	ref, err := name.NewDigest(dst)
	Expect(err).ToNot(HaveOccurred())

	err = remote.WriteLayer(ref.Context(), layer)
	Expect(err).ToNot(HaveOccurred())

	got, err := remote.Layer(ref)
	Expect(err).ToNot(HaveOccurred())
	gotHash, err := got.Digest()
	Expect(err).ToNot(HaveOccurred())
	Expect(gotHash).To(Equal(digest))
	hash, err := layer.Digest()
	Expect(err).ToNot(HaveOccurred())

	return getImageSpec(hash.String())
}

func getImageSpec(digest string) manifestTypes.ImageSpec {
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
