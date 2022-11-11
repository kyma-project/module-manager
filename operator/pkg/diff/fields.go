package diff

import (
	"bytes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/structured-merge-diff/v4/fieldpath"
)

// EmptyFields represents a set with no paths
// It looks like metav1.Fields{Raw: []byte("{}")}.
var EmptyFields = func() metav1.FieldsV1 {
	f, err := SetToFields(*fieldpath.NewSet())
	if err != nil {
		panic("should never happen")
	}
	return f
}()

// FieldsToSet creates a set paths from an input trie of fields.
func FieldsToSet(f metav1.FieldsV1) (s fieldpath.Set, err error) {
	err = s.FromJSON(bytes.NewReader(f.Raw))
	return s, err
}

// SetToFields creates a trie of fields from an input set of paths.
func SetToFields(s fieldpath.Set) (f metav1.FieldsV1, err error) {
	f.Raw, err = s.ToJSON()
	return f, err
}
