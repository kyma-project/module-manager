package api

import (
	"github.com/kyma-project/module-manager/api/v1alpha1"
	"github.com/kyma-project/module-manager/api/v1beta1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
)

func AddToScheme(s *runtime.Scheme) error {
	if err := v1alpha1.AddToScheme(s); err != nil {
		return err
	}
	if err := v1beta1.AddToScheme(s); err != nil {
		return err
	}
	if err := s.SetVersionPriority(v1beta1.GroupVersion, v1alpha1.GroupVersion); err != nil {
		return err
	}
	if err := s.AddConversionFunc(&v1alpha1.Manifest{}, &v1beta1.Manifest{},
		func(a, b interface{}, scope conversion.Scope) error {
			return a.(*v1alpha1.Manifest).ConvertTo(b.(*v1beta1.Manifest))
		}); err != nil {
		return err
	}
	return nil
}
