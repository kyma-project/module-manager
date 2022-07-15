package controllers

import (
	"reflect"
	"testing"
)

func Test_getConfigAndOverridesForInstall(t *testing.T) {
	type args struct {
		installName string
		configs     []interface{}
	}
	tests := []struct {
		name              string
		args              args
		expectedConfig    map[string]interface{}
		expectedOverrides map[string]interface{}
		wantErr           bool
	}{
		{
			name: "test config contains helm overrides and clientConfig",
			args: args{
				installName: "helm-chart",
				configs: []interface{}{
					map[string]interface{}{
						"name":         "helm-chart",
						"clientConfig": "CreateNamespace=true,Namespace=jet-cert",
						"overrides":    "x=1,fullnameOverride=override-name"},
				},
			},
			expectedConfig:    map[string]interface{}{"CreateNamespace": true, "Namespace": "jet-cert"},
			expectedOverrides: map[string]interface{}{"x": int64(1), "fullnameOverride": "override-name"},
			wantErr:           false,
		},
		{
			name: "test config contains only helm overrides",
			args: args{
				installName: "helm-chart",
				configs: []interface{}{
					map[string]interface{}{
						"name":      "helm-chart",
						"overrides": "x=1,fullnameOverride=override-name"},
				},
			},
			expectedConfig:    map[string]interface{}{},
			expectedOverrides: map[string]interface{}{"x": int64(1), "fullnameOverride": "override-name"},
			wantErr:           false,
		},
		{
			name: "test config contains only helm clientConfig",
			args: args{
				installName: "helm-chart",
				configs: []interface{}{
					map[string]interface{}{
						"name":         "helm-chart",
						"clientConfig": "CreateNamespace=true,Namespace=jet-cert"},
				},
			},
			expectedConfig:    map[string]interface{}{"CreateNamespace": true, "Namespace": "jet-cert"},
			expectedOverrides: map[string]interface{}{},
			wantErr:           false,
		},
		{
			name: "test config installName mismatch",
			args: args{
				installName: "helm-chart",
				configs: []interface{}{
					map[string]interface{}{
						"name":         "another-helm-chart",
						"clientConfig": "CreateNamespace=true,Namespace=jet-cert",
						"overrides":    "x=1,fullnameOverride=override-name"},
				},
			},
			expectedConfig:    map[string]interface{}{},
			expectedOverrides: map[string]interface{}{},
			wantErr:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := getConfigAndOverridesForInstall(tt.args.installName, tt.args.configs)
			if (err != nil) != tt.wantErr {
				t.Errorf("getConfigAndOverridesForInstall() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expectedConfig) {
				t.Errorf("getConfigAndOverridesForInstall() got = %v, config %v", got, tt.expectedConfig)
			}
			if !reflect.DeepEqual(got1, tt.expectedOverrides) {
				t.Errorf("getConfigAndOverridesForInstall() got1 = %v, overrides %v", got1, tt.expectedOverrides)
			}
		})
	}
}
