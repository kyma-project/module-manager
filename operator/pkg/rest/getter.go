package rest

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
)

type ManifestRESTClientGetter struct {
	config     *rest.Config
	cache      types.HelmClientCache
	kymaObjKey client.ObjectKey
}

func NewRESTClientGetter(config *rest.Config, cache types.HelmClientCache, kymaObjKey client.ObjectKey) *ManifestRESTClientGetter {
	return &ManifestRESTClientGetter{
		config:     config,
		cache:      cache,
		kymaObjKey: kymaObjKey,
	}
}

func (c *ManifestRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return c.config, nil
}

func (c *ManifestRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	if c.kymaObjKey.Name != "" {
		if client := c.cache.GetMemCachedClient(c.kymaObjKey); client != nil {
			return client, nil
		}
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

	memCachedClient := memory.NewMemCacheClient(discoveryClient)
	if c.kymaObjKey.Name != "" {
		c.cache.SetMemCachedClient(c.kymaObjKey, memCachedClient)
	}

	return memCachedClient, nil
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
