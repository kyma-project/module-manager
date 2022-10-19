package controllers_test

import (
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	_ "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
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
	f, err := os.Open("./test_samples/compressed.tgz")
	if err != nil {
		return nil, err
	}
	return io.NopCloser(f), nil
}

func CreateFakeOCIRegistry() string {
	// create registry and server
	layer, err := partial.CompressedToLayer(mockLayer{})
	Expect(err).ToNot(HaveOccurred())
	digest, err := layer.Digest()
	Expect(err).ToNot(HaveOccurred())

	// Set up a fake registry and write what we pulled to it.
	u, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())

	dst := fmt.Sprintf("%s/some/name@%s", u.Host, digest)
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

	return hash.String()
}

func createManifestObj(name string, spec v1alpha1.ManifestSpec) *v1alpha1.Manifest {
	return &v1alpha1.Manifest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       v1alpha1.ManifestKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: spec,
	}
}
