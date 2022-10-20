package declarative

import (
	"errors"
	"fmt"

	"github.com/kyma-project/module-manager/operator/pkg/util"

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

	errMsgSpec      = "`spec` does not exist in `%s`"
	ErrMsgMandatory = "invalid type conversion for `%s` or does not exist in spec "
	infoMsgOptional = "invalid type conversion for `%s` or optional field is not given in spec"
)

// DefaultManifestResolver represents the chart information for the passed BaseCustomObject resource.
type DefaultManifestResolver struct{}

// Get returns the chart information to be processed.
func (m DefaultManifestResolver) Get(
	object types.BaseCustomObject,
	logger logr.Logger,
) (types.InstallationSpec, error) {
	objectString := client.ObjectKeyFromObject(object).String()

	// Cast object to unstructured
	unstructuredObj, err := assertUnstructured(object)
	if err != nil {
		return types.InstallationSpec{}, err
	}

	// Get spec of object
	spec, valid := unstructuredObj.Object[specKey].(map[string]interface{})
	if !valid {
		return types.InstallationSpec{}, fmt.Errorf(errMsgSpec, objectString)
	}

	// Mandatory spec
	chartPath, valid := spec[chartPathKey].(string)
	if !valid || chartPath == "" {
		return types.InstallationSpec{}, &ResolveError{
			ObjectName: objectString,
			Err:        errors.New(ErrMsgMandatory),
		}
	}

	// Optional spec
	releaseName, valid := spec[releaseNameKey].(string)
	if !valid {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf(infoMsgOptional, releaseNameKey))
	}
	chartFlags, valid := spec[chartFlagsKey].(types.ChartFlags)
	if !valid {
		logger.V(util.DebugLogLevel).Info(fmt.Sprintf(infoMsgOptional, chartFlagsKey))
	}

	return types.InstallationSpec{
		ChartPath:   chartPath,
		ReleaseName: releaseName,
		ChartFlags:  chartFlags,
	}, nil
}

func assertUnstructured(object types.BaseCustomObject) (*unstructured.Unstructured, error) {
	objKey := client.ObjectKeyFromObject(object)
	unstructuredObj := &unstructured.Unstructured{}
	var err error

	switch typedObject := object.(type) {
	case types.CustomObject:
		unstructuredObj.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
		if err != nil {
			return nil, fmt.Errorf("invalid type conversion for `%v`: %w", objKey, err)
		}
	case *unstructured.Unstructured:
		unstructuredObj = typedObject
	default:
		return nil, fmt.Errorf("no matching type for `%v`", objKey)
	}
	return unstructuredObj, nil
}

type ResolveError struct {
	ObjectName string
	Err        error
}

func (r *ResolveError) Error() string {
	return fmt.Sprintf("Error resolving object `%s`: err %v", r.ObjectName, r.Err)
}
