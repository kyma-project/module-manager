package util

import (
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/kube"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	yaml2 "sigs.k8s.io/yaml"
)

func GetNamespaceObjBytes(clientNs string) ([]byte, error) {
	namespace := v1.Namespace{
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
	return yaml2.Marshal(namespace)
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

		// TODO: Adapt standard labels / annotations here

		requireUpdate.Append(info)
		return nil
	})

	return requireUpdate, err
}

func GetConfig(kubeConfig string, explicitPath string) (*rest.Config, error) {
	logger := ctrl.Log.WithName("getRestConfig")
	if kubeConfig != "" {
		// parameter string
		return clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
			logger.Info("Found config from passed kubeconfig")
			return clientcmd.Load([]byte(kubeConfig))
		})
	}
	// in-cluster config
	config, err := rest.InClusterConfig()
	if err == nil {
		logger.Info("Found config in-cluster")
		return config, err
	}

	// kubeconfig flag
	if flag.Lookup("kubeconfig") != nil {
		if kubeconfig := flag.Lookup("kubeconfig").Value.String(); kubeconfig != "" {
			logger.Info("Found config from flags")
			return clientcmd.BuildConfigFromFlags("", kubeconfig)
		}
	}

	// env variable
	if len(os.Getenv("KUBECONFIG")) > 0 {
		logger.Info("Found config from env")
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

	logger.Info(fmt.Sprintf("Found config file in: %s", clientConfig.ConfigAccess().GetDefaultFilename()))
	return config, nil
}

func CleanFilePathJoin(root, dest string) (string, error) {
	// On Windows, this is a drive separator. On UNIX-like, this is the path list separator.
	// In neither case do we want to trust a TAR that contains these.
	if strings.Contains(dest, ":") {
		return "", errors.New("path contains ':', which is illegal")
	}

	// The Go tar library does not convert separators for us.
	// We assume here, as we do elsewhere, that `\\` means a Windows path.
	dest = strings.ReplaceAll(dest, "\\", "/")

	// We want to alert the user that something bad was attempted. Cleaning it
	// is not a good practice.
	for _, part := range strings.Split(dest, "/") {
		if part == ".." {
			return "", errors.New("path contains '..', which is illegal")
		}
	}

	// If a path is absolute, the creator of the TAR is doing something shady.
	if path.IsAbs(dest) {
		return "", errors.New("path is absolute, which is illegal")
	}

	// SecureJoin will do some cleaning, as well as some rudimentary checking of symlinks.
	newpath, err := securejoin.SecureJoin(root, dest)
	if err != nil {
		return "", err
	}

	return filepath.ToSlash(newpath), nil
}
