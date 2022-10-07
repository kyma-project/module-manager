# module-manager

The [module-manager](#module-manager) plays an important part in the modularization ecosystem to handle installation of resources from control-plane to runtime clusters. 
For more information, refer to the [architecture summary](https://github.com/kyma-project/lifecycle-manager#architecture).
This repository offers an **_operator_** and relevant **_library packages_** to perform reconciliation of helm chart resources, configurations and custom resource state handling.
The **_library package_** could be used independently of the operator.

### Content

* [Operator](#operator)
* [Manifest library](#manifest-library) 
* [Local setup](#local-setup)
* [Cluster setup](#cluster-setup)
* [Contribution](#contribution)
* [Versioning and releasing](#versioning-and-releasing)
* [Next steps](#next-steps)

**Disclaimer**: This repository is still in development and implementation details could change rapidly.

### Operator

The module-manager reconciles the `Manifest` custom resource.
As observed in the [Sample Manifest CR](operator/config/samples/operator_v1alpha1_manifest.yaml) and [API definition](operator/api/v1alpha1/manifest_types.go), 
the `Spec` contains the necessary properties to process installations.

| Spec field | Description                                                                                                   |
|------------|---------------------------------------------------------------------------------------------------------------|
| Remote     | `false` for single cluster mode, `true`(default) for dual cluster mode                                        |
| Resource   | Additional unstructured custom resource to be installed, e.g. for implicit reconciliation via Installs        |
| Installs   | OCI image specification for a list of helm charts                                                             |
| Config     | Optional: OCI image specification for helm configuration and set flags                                        |
| CRDs       | Optional: OCI image specification for additional CRDs that are pre-installed before helm charts are processed |

```yaml
apiVersion: operator.kyma-project.io/v1alpha1
kind: Manifest
metadata:
  labels:
    operator.kyma-project.io/channel: stable
    operator.kyma-project.io/controller-name: manifest
    operator.kyma-project.io/kyma-name: kyma-sample
  name: manifestkyma-sample-delete
  namespace: default
spec:
  remote: true
  resource:
    kind: SampleCRD
    resource: samplecrds
    apiVersion: operator.kyma-project.io/v1alpha1
    metadata:
      name: sample-crd-from-manifest
      namespace: default
    spec:
      randomkey: samplevalue
  crds:
    ref: sha256:71cf4f1fee1a2f51296cc805aa9b24bc14fd5c2b4aee1e24aadc2996b067bb3d
    name: kyma-project.io/module/example-module-name
    repo: kcp-registry.localhost:8888/component-descriptors
    type: oci-ref
  config:
    ref: sha256:61be4f1fee1a2f51296cc805aa9b24bc14fd5c2b4aee1e24aadc2996b067ccec
    name: kyma-project.io/module/example-module-name
    repo: kcp-registry.localhost:8888/component-descriptors
    type: oci-ref
  installs:
    - source:
        name: kyma-project.io/module/example-module-name
        repo: kcp-registry.localhost:8888/component-descriptors
        ref: sha256:c64f0580a74259712f24243528881a76b5e1c9cd254fa58197de93a6347f99b9
        type: oci-ref
      name: redis
```

If `.Spec.Remote.` is set to `true`, the operator will look for a secret with the name specified by Manifest CR's label `operator.kyma-project.io/kyma-name: kyma-sample`. 
Follow steps in this [guide](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/creating-test-environment.md#install-kyma-and-run-lifecycle-manager-operator) to create the required secret.

For more details on OCI Image **bundling** and **formats**, refer to our [bundling and installation guide](https://github.com/kyma-project/lifecycle-manager/tree/main/samples/template-operator#bundling-and-installation).
The component-descriptor generated from this guide could be used for `Manifest Spec` OCI image specifications.

>Note: Alternatively you could use your own bundled OCI images. Please take care to conform to [.Spec.Config](https://github.com/kyma-project/lifecycle-manager/blob/main/samples/template-operator/config.yaml) format, 
> which corresponds to helm configuration and set value flags for an installation in `.Spec.Installs[].Name`.

### Manifest library
The operator uses the [manifest library](https://pkg.go.dev/github.com/kyma-project/module-manager/operator/pkg/manifest) to process deployments on clusters.

It supports helm chart installations from two sources: **_helm repositories_** and **_local paths_**. Additionally, it helps to process additional local installations of CRs, CRDs and custom checks.
The library could be used to simply process deployments on target clusters or use withing your own operator to carry deployment operations. 
E.g. [template-operator](https://github.com/kyma-project/lifecycle-manager/tree/main/samples/template-operator) used this library (via the [declarative](operator/pkg/declarative) library) to perform necessary operations on target clusters during reconciliations.
Import package `github.com/kyma-project/module-manager/operator/pkg/manifest` to include the main functionality required to process helm charts coupled with additional state handling.
For more options and information refer to the [InstallInfo](operator/pkg/manifest/operations.go) type definition.

Sample usage:

```go
package sample
// Sample usage of chart installation via local chart path

import (
    "k8s.io/client-go/rest"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "github.com/kyma-project/module-manager/operator/pkg/manifest"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/custom"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var restConfig *rest.Config
var client client.Client

deployInfo := manifest.InstallInfo{
    Ctx: ctx,
    ChartInfo: &manifest.ChartInfo{
        ChartPath:   "/chart/path",
        Flags:       types.ChartFlags{
            // ConfigFlags support string, bool and int types as helm chart flags
            // check: https://github.com/helm/helm/blob/d7b4c38c42cb0b77f1bcebf9bb4ae7695a10da0b/pkg/action/install.go#L67
            ConfigFlags: types.Flags{ 
                "Namespace":       chartNs,
                "CreateNamespace": true,
            },
            // SetFlags are chart value overrides
            SetFlags: types.Flags{
                ".some.value.override": "override",
            },      
        },
    },
    ClusterInfo: custom.ClusterInfo{
        Config: restConfig, // destination cluster rest config
        Client: client, // destination cluster rest client
    },
    ResourceInfo: manifest.ResourceInfo{
        CustomResources: []*unstructured.Unstructured{} // additional custom resources to be installed
        BaseResource: unstructured.Unstructured{}, // base resource to be passed for custom checks, usually the reconciled resource
		Crds: []*apiextensions.CustomResourceDefinition // additional custom resource definitions to be installed
    },
    CheckReadyStates: true,
}

ready, err := manifest.InstallChart(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
	return false, err
}

ready, err := manifest.UninstallChart(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
    return false, err
}

ready, err := manifest.ConsistencyCheck(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
    return false, err
}

```

### Local setup

- Refer to the local cluster and registry setup [documentation](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/provision-cluster-and-registry.md#local-cluster-setup) to create cluster(s) either in a single-cluster or dual-cluster mode.
- Set your `KUBECONFIG` environment variable to point towards the desired cluster.
- Run the following `make` file commands:

| Make command | Description                                      |
|--------------|--------------------------------------------------|
| build        | Run fmt, vet and DeepCopy method implementations |
| manifests    | Create CRDs based on [API types](operator/api)   |
| install      | Install CRDs                                     |
| run          | Run operator controller locally                  |

### Cluster setup

- Refer to the remote cluster and registry setup [documentation](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/provision-cluster-and-registry.md#remote-cluster-setup) to create cluster(s) either in a single-cluster or dual-cluster mode.
- Set your `KUBECONFIG` environment variable to point towards the desired cluster.
- Run the following commands with `IMG` environment variable pointing to the image name

| Make command | Description                                           |
|--------------|-------------------------------------------------------|
| docker-build | Build operator image                                  |
| docker-push  | Push docker image to your repo                        |
| deploy       | Deploys the operator resources to the desired cluster |

### Contribution

Please open issues and pull requests to address both bugs and feature requests to this repository. Any additional feedback could also be addressed directly to the contributors.

### Versioning and Releasing
Refer to a summary on versioning [here](https://github.com/kyma-project/lifecycle-manager#versioning-and-releasing).

### Next Steps
- Kustomize support for installation of resources
- Improved testing strategy, including integration with other operators in the modularization ecosystem

