package descriptor

import (
	"encoding/json"
	"fmt"
	"github.com/alecthomas/jsonschema"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
)

var imageSpecSchema *gojsonschema.Schema
var helmChartSpecSchema *gojsonschema.Schema

func InitializeSchemaValidators() error {
	imageSpecJsonBytes := jsonschema.Reflect(v1alpha1.ImageSpec{})
	bytes, err := imageSpecJsonBytes.MarshalJSON()
	if err != nil {
		return err
	}

	imageSpecSchema, err = gojsonschema.NewSchema(gojsonschema.NewBytesLoader(bytes))
	if err != nil {
		return err
	}

	helmChartSpecJsonBytes := jsonschema.Reflect(v1alpha1.HelmChartSpec{})
	bytes, err = helmChartSpecJsonBytes.MarshalJSON()
	if err != nil {
		return err
	}

	helmChartSpecSchema, err = gojsonschema.NewSchema(gojsonschema.NewBytesLoader(bytes))
	if err != nil {
		return err
	}
	return nil
}

func GetSpecType(data []byte) (v1alpha1.RefTypeMetadata, error) {
	raw := make(map[string]json.RawMessage)
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", err
	}

	var refType v1alpha1.RefTypeMetadata
	if err := yaml.Unmarshal(raw["type"], &refType); err != nil {
		return "", err
	}

	return refType, nil
}

func Decode(data []byte, obj interface{}, refType v1alpha1.RefTypeMetadata) error {
	if err := Validate(data, refType); err != nil {
		return err
	}

	if err := yaml.Unmarshal(data, &obj); err != nil {
		return err
	}

	return nil
}

func Validate(data []byte, refType v1alpha1.RefTypeMetadata) error {
	dataBytes := gojsonschema.NewBytesLoader(data)
	var result *gojsonschema.Result
	var err error

	switch refType {
	case v1alpha1.HelmChartType:
		result, err = helmChartSpecSchema.Validate(dataBytes)
		if err != nil {
			return err
		}

	case v1alpha1.OciRefType:
		result, err = imageSpecSchema.Validate(dataBytes)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported type %s passed as installation type", refType)
	}

	if !result.Valid() {
		errorString := ""
		for _, err := range result.Errors() {
			errorString = fmt.Sprintf("%s: %s", errorString, err.String())
		}
		return fmt.Errorf(errorString)
	}
	return nil
}
