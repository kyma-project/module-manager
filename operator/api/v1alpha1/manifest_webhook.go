/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"github.com/kyma-project/module-manager/operator/pkg/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var manifestlog = logf.Log.WithName("manifest-resource") //nolint:gochecknoglobals

func (m *Manifest) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(m).
		Complete()
}

//nolint:lll
//+kubebuilder:webhook:path=/mutate-component-kyma-project-io-v1alpha1-manifest,mutating=true,failurePolicy=fail,sideEffects=None,groups=component.kyma-project.io,resources=manifests,verbs=create;update,versions=v1alpha1,name=mmanifest.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Manifest{}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (m *Manifest) Default() {
	var emptyImageSpec types.ImageSpec
	if m.Spec.Config == emptyImageSpec {
		m.Spec.Config = types.ImageSpec{}
	}

	if m.Spec.Installs == nil {
		m.Spec.Installs = make([]InstallInfo, 0)
	}
}

//nolint:lll
//+kubebuilder:webhook:path=/validate-component-kyma-project-io-v1alpha1-manifest, mutating=false,failurePolicy=fail,sideEffects=None,groups=component.kyma-project.io,resources=manifests,verbs=create;update,versions=v1alpha1,name=vmanifest.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Manifest{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (m *Manifest) ValidateCreate() error {
	manifestlog.Info("validate create", "name", m.Name)

	return m.validateInstalls()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (m *Manifest) ValidateUpdate(old runtime.Object) error {
	manifestlog.Info("validate update", "name", m.Name)

	return m.validateInstalls()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (m *Manifest) ValidateDelete() error {
	manifestlog.Info("validate delete", "name", m.Name)
	return nil
}

func (m *Manifest) validateInstalls() error {
	fieldErrors := make(field.ErrorList, 0)

	codec, err := types.NewCodec()
	if err != nil {
		fieldErrors = append(fieldErrors,
			field.Invalid(field.NewPath("spec").Child("installs"),
				"validator initialize", err.Error()))
	}

	if len(fieldErrors) == 0 {
		for _, install := range m.Spec.Installs {
			specType, err := types.GetSpecType(install.Source.Raw)
			if err != nil {
				fieldErrors = append(fieldErrors,
					field.Invalid(field.NewPath("spec").Child("installs"),
						install.Source.Raw, err.Error()))
				continue
			}

			err = codec.Validate(install.Source.Raw, specType)
			if err != nil {
				fieldErrors = append(fieldErrors,
					field.Invalid(field.NewPath("spec").Child("installs"),
						install.Source.Raw, err.Error()))
			}
		}
	}

	if len(fieldErrors) > 0 {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: ManifestKind},
			m.Name, fieldErrors)
	}

	return nil
}
