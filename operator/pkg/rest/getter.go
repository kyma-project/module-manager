package rest

import (
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type ManifestRESTClientGetter struct {
	config          *rest.Config
	client          discovery.CachedDiscoveryInterface
	clientSyncMutex sync.Mutex
}

func NewRESTClientGetter(config *rest.Config) *ManifestRESTClientGetter {
	return &ManifestRESTClientGetter{
		config:          config,
		clientSyncMutex: sync.Mutex{},
	}
}

func (c *ManifestRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return c.config, nil
}

func (c *ManifestRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	c.clientSyncMutex.Lock()
	defer c.clientSyncMutex.Unlock()

	if c.client != nil {
		return c.client, nil
	}

	config, err := c.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	// The more groups you have, the more discovery requests you need to make.
	// given 25 groups (our groups + a few custom conf) with one-ish version each, discovery needs to make 50 requests
	// double it just so we don't end up here again for a while.  This config is only used for discovery.
	config.Burst = 100

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	c.client = memory.NewMemCacheClient(discoveryClient)

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
