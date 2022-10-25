package rest

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type ManifestRESTClientGetter struct {
	config *rest.Config
	client discovery.CachedDiscoveryInterface
}

// NewRESTClientGetter returns a RESTClientGetter instance based on the passed rest config.
// Additionally, it always returns the cached client passed during struct initialization.
// To invalidate the cached client, call client.Invalidate()
func NewRESTClientGetter(config *rest.Config, memCachedClient discovery.CachedDiscoveryInterface,
) *ManifestRESTClientGetter {
	return &ManifestRESTClientGetter{
		config: config,
		client: memCachedClient,
	}
}

func (c *ManifestRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return c.config, nil
}

func (c *ManifestRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return c.client, nil
}

func (c *ManifestRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := c.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
	return expander, nil
}

func (c *ManifestRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}
