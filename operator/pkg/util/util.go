package util

import (
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"path"
	yaml2 "sigs.k8s.io/yaml"
)

func GetNamespaceObjBytes(clientNs string) ([]byte, error) {
	ns := v1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clientNs,
			Labels: map[string]string{
				"name": clientNs,
			},
		},
	}
	return yaml2.Marshal(ns)
}

func FilterExistingResources(resources kube.ResourceList) (kube.ResourceList, error) {
	var requireUpdate kube.ResourceList

	err := resources.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		helper := resource.NewHelper(info.Client, info.Mapping)
		_, err = helper.Get(info.Namespace, info.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return errors.Wrapf(err, "could not get information about the resource %s / %s", info.Name, info.Namespace)
		}

		//TODO: Adapt standard labels / annotations here

		requireUpdate.Append(info)
		return nil
	})

	return requireUpdate, err
}

func GetConfig(kubeConfig string, explicitPath string) (*rest.Config, error) {
	if kubeConfig != "" {
		// parameter string
		return clientcmd.BuildConfigFromKubeconfigGetter("", func() (config *clientcmdapi.Config, e error) {
			fmt.Println("Found config from passed kubeconfig")
			return clientcmd.Load([]byte(kubeConfig))
		})
	}
	// in-cluster config
	config, err := rest.InClusterConfig()
	if err == nil {
		fmt.Println("Found config in-cluster")
		return config, err
	}

	// kubeconfig flag
	if flag.Lookup("kubeconfig") != nil {
		if kubeconfig := flag.Lookup("kubeconfig").Value.String(); kubeconfig != "" {
			fmt.Println("Found config from flags")
			return clientcmd.BuildConfigFromFlags("", kubeconfig)
		}
	}

	// env variable
	if len(os.Getenv("KUBECONFIG")) > 0 {
		fmt.Println("Found config from env")
		return clientcmd.BuildConfigFromFlags("masterURL", os.Getenv("KUBECONFIG"))
	}

	// default directory + working directory + explicit path -> merged
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = explicitPath
	pwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error reading current working directory %w", err)
	}
	loadingRules.Precedence = append(loadingRules.Precedence, path.Join(pwd, ".kubeconfig"))
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	config, err = clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	fmt.Printf("Found config file in: %s", clientConfig.ConfigAccess().GetDefaultFilename())
	return config, nil
}
