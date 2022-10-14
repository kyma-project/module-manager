package declarative

import (
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	specKey        = "spec"
	chartPathKey   = "chartPath"
	releaseNameKey = "releaseName"
	chartFlagsKey  = "chartFlags"

	errMsgMandatory = "invalid type conversion for %s or does not exist in spec "
	infoMsgOptional = "invalid type conversion for %s or optional field is not given in spec"
)

// ManifestResolver represents the chart information for the passed TestCRD resource.
type DefaultManifestResolver struct{}

// Get returns the chart information to be processed.
func (m DefaultManifestResolver) Get(object types.BaseCustomObject, logger logr.Logger) (types.InstallationSpec, error) {

	objKey := client.ObjectKeyFromObject(object)
	// Cast object to unstructured
	unstructuredObj := &unstructured.Unstructured{}
	var err error
	switch typedObject := object.(type) {
	case types.CustomObject:
		unstructuredObj.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
		if err != nil {
			return types.InstallationSpec{}, fmt.Errorf("invalid type conversion for `%v`: %w", objKey, err)
		}
	case *unstructured.Unstructured:
		unstructuredObj = typedObject
	default:
		return types.InstallationSpec{}, fmt.Errorf("no matching type for `%v`", objKey)
	}

	// Get spec of object
	spec, ok := unstructuredObj.Object[specKey].(map[string]interface{})
	if !ok {
		return types.InstallationSpec{}, fmt.Errorf(errMsgMandatory, chartPathKey)
	}

	// Mandatory
	chartPath, ok := spec[chartPathKey].(string)
	if !ok || chartPath == "" {
		return types.InstallationSpec{}, &ResolveError{
			objectName: objKey.String(),
			Err:        errors.New(errMsgMandatory),
		}
	}

	// Optional
	releaseName, ok := spec[releaseNameKey].(string)
	if !ok {
		logger.V(2).Info(fmt.Sprintf(infoMsgOptional, releaseNameKey))
	}
	chartFlags, ok := spec[chartFlagsKey].(types.ChartFlags)
	if !ok {
		logger.V(2).Info(fmt.Sprintf(infoMsgOptional, chartFlagsKey))
	}

	return types.InstallationSpec{
		ChartPath:   chartPath,
		ReleaseName: releaseName,
		ChartFlags:  chartFlags,
	}, nil
}

type ResolveError struct {
	objectName string
	Err        error
}

func (r *ResolveError) Error() string {
	return fmt.Sprintf("Error resolving object `%s`: err %v", r.objectName, r.Err)
}
