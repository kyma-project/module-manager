# Module Manager

## Contents

* [Overview](#overview)
* [Stability](#stability)
* [Operator specification](#operator-specification)
  * [Manifest Custom Resource](#manifest-custom-resource)
  * [Sample resource](#sample-resource)
* [Manifest library](#manifest-library)
  * [Sample usage](#sample-usage)
* [Run the operator](#run-the-operator)
  * [Local setup](#local-setup)
  * [Cluster setup](#cluster-setup)
* [Contribution](#contribution)
* [Versioning and releasing](#versioning-and-releasing)

## Overview

[Module Manager](#module-manager) plays an important part in the modularization ecosystem to handle installation of resources from Control Plane to runtime clusters.
As a meta-controller, it manages the entire installation lifecycle of individual modules from installation, upgrades to cleanup.
For more information, see the [architecture summary](https://github.com/kyma-project/lifecycle-manager#architecture).

The Module Manager repository contains:
* An **operator**, which reconciles [Manifest](https://github.com/kyma-project/module-manager/blob/main/api/v1alpha1/manifest_types.go)
* The relevant **library packages** to perform reconciliation of resources, configurations, and custom resource state handling. 
  The library packages be used independently of the operator and is consumed as a helper library by other modules in the Kyma ecosystem.

## Stability

The architecture and implementation are based on proof-of-concept (POC) results and can change rapidly as we work towards providing a stable solution.
In general, the reconciliation and resource-processing framework is considered stable and ready to be used.

The Module Manager repository offers the following components:

| System Component                                          | Stability                                                                                         |
|-----------------------------------------------------------|---------------------------------------------------------------------------------------------------|
| [Manifest](api/v1alpha1/manifest_types.go)       | Alpha-Grade - do not rely on automation and watch upstream as close as possible                   |
| [Controller](internal/controllers/manifest_controller.go) | In active development - expect bugs and fast-paced development                                    |
| [Library](pkg)                                   | In active development - expect bugs and fast-paced development. Detailed documentation to follow. |

## Operator specification

Module Manager reconciles the `Manifest` custom resource.
The `Spec` contains the necessary properties to process installations as seen in the example [Sample Manifest CR](config/samples/operator_v1alpha1_manifest.yaml) and [API definition](api/v1alpha1/manifest_types.go) below.

### Manifest custom resource

| Spec field | Description                                                                                                   |
|------------|---------------------------------------------------------------------------------------------------------------|
| Remote     | `false` for single-cluster mode, `true`(default) for dual-cluster mode                                        |
| Resource   | Additional unstructured custom resource to be installed, used for implicit reconciliation via Installs        |
| Installs   | OCI image specification for a list of Helm charts                                                             |
| Config     | Optional: OCI image specification for Helm configuration and set flags                                        |
| CRDs       | Optional: OCI image specification for additional CRDs that are pre-installed before Helm charts are processed |

If `.Spec.Remote.` is set to `true`, the operator looks for a secret with the name specified by Manifest CR's label `operator.kyma-project.io/kyma-name: kyma-sample`.
This secret is used to connect to an existing cluster (target) for `Manifest` resource installations.
Learn how to create the required secret in [Install Kyma and run lifecycle-manager operator](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/creating-test-environment.md#install-kyma-and-run-lifecycle-manager-operator).

For more details on OCI Image **bundling** and **formats**, read our [bundling and installation guide](https://github.com/kyma-project/template-operator#bundling-and-installation).
You can use the component descriptor generated from this guide to independently build a `Manifest Spec` based on the OCI image specifications.

>**NOTE:** [Lifecycle-Manager](https://github.com/kyma-project/lifecycle-manager#how-it-works) translates these layers from a `ModuleTemplate` resource on the Kyma Control Plane (KCP) and translates them automatically to a subsequent `Manifest` resource.
>Alternatively, you can use your own bundled OCI images. If using additional Helm configuration, you must conform to [.Spec.Config](https://github.com/kyma-project/template-operator/blob/main/config.yaml) format, corresponding to Helm `installation` and `set` value flags, for an installation in `.Spec.Installs[].Name`.

### Sample resource
<details>
<summary><b>Example</b></summary>

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
    - source:
        chartName: mysql
        url: https://charts.bitnami.com/bitnami
        type: helm-chart
      name: bitnami
```
</details>

## Manifest library

The operator uses the [manifest library](https://pkg.go.dev/github.com/kyma-project/module-manager/pkg/manifest) to process deployments on clusters.

The manifest library supports Helm chart installations from two sources: **helm repositories** and **local paths**. Additionally, it helps to process local installations of CRs, CRDs and custom state checks.

>**NOTE:** We plan to offer [Kustomize installation support](https://github.com/kyma-project/module-manager/issues/124), along with the existing helm installation soon. 

Use the manifest library to simply process deployments on target clusters, or use it within your own operator to process deployment operations.
For example, [template-operator](https://github.com/kyma-project/template-operator) uses the manifest library (through the [declarative](pkg/declarative) library) to perform necessary operations on target clusters during reconciliations.
To get started, simply import package `github.com/kyma-project/module-manager/pkg/manifest` to include the main functionality provided by the library to process Helm charts, coupled with additional state handling.
For more options and information, read the [InstallInfo](pkg/manifest/operations.go) type definition.

### Sample usage
<details>
<summary><b>Example</b></summary>

```go
package sample
// Sample usage of chart installation via local chart path

import (
    "k8s.io/client-go/rest"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "github.com/kyma-project/module-manager/pkg/manifest"
	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/custom"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var restConfig *rest.Config
var client client.Client

deployInfo := manifest.InstallInfo{
    Ctx: ctx,
    ChartInfo: &manifest.ChartInfo{
        ChartPath:   "/chart/path",
        Flags:       types.ChartFlags{
            ConfigFlags: types.Flags{ // optional: ConfigFlags support string, bool and int types as Helm chart flags
                // check: https://github.com/helm/helm/blob/d7b4c38c42cb0b77f1bcebf9bb4ae7695a10da0b/pkg/action/install.go#L67
                "Namespace":       chartNs,
                "CreateNamespace": true,
            },
            SetFlags: types.Flags{ // optional: SetFlags are chart value overrides
                ".some.value.override": "override",
            },      
        },
    },
    ClusterInfo: custom.ClusterInfo{
        Config: restConfig, // destination cluster rest config
        Client: client, // destination cluster rest client
    },
    ResourceInfo: manifest.ResourceInfo{
        CustomResources: []*unstructured.Unstructured{}, // optional: additional custom resources to be installed
        BaseResource: unstructured.Unstructured{}, // base resource to be reconciled, also passed for custom state checks e.g. Manifest CR
		Crds: []*apiextensions.CustomResourceDefinition // optional: additional custom resource definitions to be installed
    },
    CheckFn: func (context.Context, *unstructured.Unstructured, *logr.Logger, ClusterInfo) (bool, error) { // optional: custom logic for resource state checks
		return true, nil
	},
    CheckReadyStates: true,
}

// Based on deployInfo, the following operations can be performed 

// Option 1: Install resources
ready, err := manifest.InstallChart(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
	return false, err
}

// Option 2: Verify resources exist
ready, err := manifest.ConsistencyCheck(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
    return false, err
}

// Option 3: Uninstall resources
ready, err := manifest.UninstallChart(logger, deployInfo, []types.ObjectTransform{})
if err != nil {
return false, err
}
```

</details>

## Run the operator 

### Local Cluster setup

1. Set up the local cluster environment according to [local cluster and registry setup](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/provision-cluster-and-registry.md#local-cluster-setup) to create cluster(s) either in a single-cluster or dual-cluster mode.
2. Set your `KUBECONFIG` environment variable to point towards the desired cluster.
3. Run the following `make` file commands:

   | Make command | Description                                          |
   |--------------|------------------------------------------------------|
   | build        | Run fmt, vet and DeepCopy method implementations     |
   | manifests    | Create RBACs CRDs based on [API types](api) |
   | install      | Install CRDs                                         |
   | run          | Run operator controller locally                      |

### Remote Cluster setup

1. Set up the remote cluster environment according to [remote cluster and registry setup](https://github.com/kyma-project/lifecycle-manager/blob/main/docs/developer/provision-cluster-and-registry.md#remote-cluster-setup) to create cluster(s) either in a single-cluster or dual-cluster mode.
2. Set your `KUBECONFIG` environment variable to point towards the desired cluster.
3. Run the following commands with `IMG` environment variable pointing to the image name

   | Make command | Description                                           |
   |--------------|-------------------------------------------------------|
   | docker-build | Build operator image                                  |
   | docker-push  | Push docker image to your repo                        |
   | deploy       | Deploys the operator resources to the desired cluster |

## Contribution
If you want to contribute, follow the [Kyma contribution guidelines](https://kyma-project.io/community/contributing/02-contributing/).

## Versioning and releasing
Learn about [versioning and releasing](https://github.com/kyma-project/lifecycle-manager#versioning-and-releasing). 

