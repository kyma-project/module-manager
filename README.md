# module-manager

This repository offers an operator and relevant packages to perform installation, update and uninstallation of helm charts.
The packages could be used independent of the operator.

**Disclaimer**: This repository is still in development and implementation details can change quickly.

## Local setup

- Spin up a k3d cluster (other types like kind are also supported)
- From the **api** folder run: 
`make install`
- From the **operator** folder run
  `make build` 
followed by
  `make run`
to run the operator
- Config your own **kubeconfig** by setting the **r.Config** variable inside */operator/controllers/manifest_controller.go*


## Use manifest packages in your own operator

- Import package `"github.com/kyma-project/module-manager/operator/pkg/manifest"`
- [Package Specifications](https://pkg.go.dev/github.com/kyma-project/module-manager/operator/pkg/manifest) 
- Package usage example:
```
    manifestOperations := manifest.NewOperations(logger, r.RestConfig, cli.New())
	if create {
		if err := manifestOperations.Install("chart/path", releaseName, "repoName/chartName", repoName, url, args); err != nil {
			return err
		}
	} else {
		if err := manifestOperations.Uninstall("chart/path", "repoName/chartName", releaseName, args); err != nil {
			return err
		}
	}
  ```
- `chartPath` takes priority over the **url** parameter. If the chart should be downloaded from `url` pass `chartPath` as empty string
- `args` only supports `--set` and [action flags](https://github.com/helm/helm/blob/v3.9.0/pkg/action/install.go#L66), with variable names and values, comma-seperated. Example:
```
  args = map[string]string{
    "set": "xyz=abc",
    "flags": "CreateNamespace=true,Namespace=new-space",
  }
  ```
