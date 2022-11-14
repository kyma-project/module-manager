package manifest

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/go-logr/logr"
	"github.com/kyma-project/module-manager/operator/pkg/util"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/util/openapi"
	openapivalidation "k8s.io/kubectl/pkg/util/openapi/validation"
	"k8s.io/kubectl/pkg/validation"
)

const (
	apis = "/apis"
	api  = "/api"
)

// SingletonClients serves as a single-minded client interface that combines
// all kubernetes Client APIs (Helm, Kustomize, Kubernetes, Client-Go) under the hood.
// It offers a simple initialization lifecycle during creation, but delegates all
// heavy duty work to deferred discovery logic and a single http client
// as well as a client cache to support GV-based clients.
type SingletonClients struct {
	httpClient *http.Client

	// the original config used for all clients
	config *rest.Config

	// discovery client, used for dynamic clients and GVK discovery
	discoveryClient     discovery.CachedDiscoveryInterface
	discoveryRESTMapper meta.ResettableRESTMapper
	// expander for GVK and REST expansion from discovery client
	discoveryShortcutExpander meta.RESTMapper

	// kubernetes client
	kubernetesClient *kubernetes.Clientset
	dynamicClient    dynamic.Interface

	// helm client with factory delegating to other clients
	helmClient *kube.Client
	install    *action.Install

	// OpenAPI document parser singleton
	openAPIParser *openapi.CachedOpenAPIParser

	// OpenAPI document getter singleton
	openAPIGetter *openapi.CachedOpenAPIGetter

	// GVK based structured Client Cache
	structuredSyncLock        sync.Mutex
	structuredRestClientCache map[string]resource.RESTClient

	// GVK based unstructured Client Cache
	unstructuredSyncLock        sync.Mutex
	unstructuredRestClientCache map[string]resource.RESTClient
}

var _ kube.Factory = &SingletonClients{}

func NewSingletonClients(config *rest.Config, logger logr.Logger) (*SingletonClients, error) {
	if err := setKubernetesDefaults(config); err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, err
	}

	discoveryConfig := *config
	discoveryConfig.Burst = 200
	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(&discoveryConfig, httpClient)
	if err != nil {
		return nil, err
	}
	cachedDiscoveryClient := memory.NewMemCacheClient(discoveryClient)
	discoveryRESTMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscoveryClient)
	discoveryShortcutExpander := restmapper.NewShortcutExpander(discoveryRESTMapper, cachedDiscoveryClient)

	kubernetesClient, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, err
	}

	openAPIGetter := openapi.NewOpenAPIGetter(cachedDiscoveryClient)

	clients := &SingletonClients{
		httpClient:                  httpClient,
		config:                      config,
		discoveryClient:             cachedDiscoveryClient,
		discoveryRESTMapper:         discoveryRESTMapper,
		discoveryShortcutExpander:   discoveryShortcutExpander,
		kubernetesClient:            kubernetesClient,
		dynamicClient:               dynamicClient,
		openAPIGetter:               openAPIGetter,
		openAPIParser:               openapi.NewOpenAPIParser(openAPIGetter),
		structuredRestClientCache:   map[string]resource.RESTClient{},
		unstructuredRestClientCache: map[string]resource.RESTClient{},
	}

	clients.helmClient = &kube.Client{
		Factory: clients,
		Log: func(msg string, args ...interface{}) {
			logger.V(util.DebugLogLevel).Info(msg+"\n", args...)
		},
		Namespace: metav1.NamespaceDefault,
	}

	// DO NOT CALL INIT
	actionConfig := new(action.Configuration)
	actionConfig.KubeClient = clients.helmClient
	actionConfig.Log = clients.helmClient.Log
	var store *storage.Storage
	var drv *driver.Memory
	if actionConfig.Releases != nil {
		if mem, ok := actionConfig.Releases.Driver.(*driver.Memory); ok {
			drv = mem
		}
	}
	if drv == nil {
		drv = driver.NewMemory()
	}
	drv.SetNamespace(metav1.NamespaceDefault)
	store = storage.Init(drv)
	actionConfig.Releases = store
	actionConfig.RESTClientGetter = clients
	clients.install = action.NewInstall(actionConfig)

	return clients, nil
}

func (f *SingletonClients) ToRESTConfig() (*rest.Config, error) {
	return f.config, nil
}

func (f *SingletonClients) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return f.discoveryClient, nil
}

func (f *SingletonClients) ToRESTMapper() (meta.RESTMapper, error) {
	return f.discoveryShortcutExpander, nil
}

func (f *SingletonClients) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}

func (f *SingletonClients) KubernetesClientSet() (*kubernetes.Clientset, error) {
	return f.kubernetesClient, nil
}

func (f *SingletonClients) DynamicClient() (dynamic.Interface, error) {
	return f.dynamicClient, nil
}

// NewBuilder returns a new resource builder for structured api objects.
func (f *SingletonClients) NewBuilder() *resource.Builder {
	return resource.NewBuilder(f)
}

func (f *SingletonClients) RESTClient() (*rest.RESTClient, error) {
	return rest.RESTClientFor(f.config)
}

func (f *SingletonClients) clientCacheKeyForMapping(mapping *meta.RESTMapping) string {
	return fmt.Sprintf("%s+%s:%s",
		mapping.Resource.String(), mapping.GroupVersionKind.String(), mapping.Scope.Name())
}

func (f *SingletonClients) ClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	f.structuredSyncLock.Lock()
	defer f.structuredSyncLock.Unlock()
	key := f.clientCacheKeyForMapping(mapping)
	client, found := f.structuredRestClientCache[key]

	if found {
		return client, nil
	}

	cfg := rest.CopyConfig(f.config)
	gvk := mapping.GroupVersionKind
	switch gvk.Group {
	case corev1.GroupName:
		cfg.APIPath = api
	default:
		cfg.APIPath = apis
	}
	gv := gvk.GroupVersion()
	cfg.GroupVersion = &gv

	var err error
	client, err = rest.RESTClientForConfigAndClient(cfg, f.httpClient)
	if err != nil {
		return nil, err
	}

	f.structuredRestClientCache[key] = client
	return client, err
}

func (f *SingletonClients) UnstructuredClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	f.unstructuredSyncLock.Lock()
	defer f.unstructuredSyncLock.Unlock()
	key := f.clientCacheKeyForMapping(mapping)
	client, found := f.unstructuredRestClientCache[key]

	if found {
		return client, nil
	}

	cfg := rest.CopyConfig(f.config)
	cfg.APIPath = apis
	if mapping.GroupVersionKind.Group == corev1.GroupName {
		cfg.APIPath = api
	}
	gv := mapping.GroupVersionKind.GroupVersion()
	cfg.ContentConfig = resource.UnstructuredPlusDefaultContentConfig()
	cfg.GroupVersion = &gv

	var err error
	client, err = rest.RESTClientForConfigAndClient(cfg, f.httpClient)
	if err != nil {
		return nil, err
	}
	f.structuredRestClientCache[key] = client
	return client, err
}

func (f *SingletonClients) ResourceInfo(obj *unstructured.Unstructured) (*resource.Info, error) {
	info := &resource.Info{}
	gvk := obj.GroupVersionKind()

	mapping, err := f.discoveryShortcutExpander.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		f.discoveryRESTMapper.Reset()
		return nil, err
	}
	clnt, err := f.ClientForMapping(mapping)
	if err != nil {
		return nil, err
	}

	info.Client = clnt
	info.Mapping = mapping
	info.Namespace = obj.GetNamespace()
	info.Name = obj.GetName()
	info.Object = obj.DeepCopyObject()
	return info, nil
}

func (f *SingletonClients) Validator(
	validationDirective string, verifier *resource.QueryParamVerifier,
) (validation.Schema, error) {
	if validationDirective == metav1.FieldValidationIgnore {
		return validation.NullSchema{}, nil
	}

	resources, err := f.OpenAPISchema()
	if err != nil {
		return nil, err
	}

	conjSchema := validation.ConjunctiveSchema{
		openapivalidation.NewSchemaValidation(resources),
		validation.NoDoubleKeySchema{},
	}
	return validation.NewParamVerifyingSchema(conjSchema, verifier, validationDirective), nil
}

// OpenAPISchema returns metadata and structural information about
// Kubernetes object definitions.
func (f *SingletonClients) OpenAPISchema() (openapi.Resources, error) {
	return f.openAPIParser.Parse()
}

func (f *SingletonClients) OpenAPIGetter() discovery.OpenAPISchemaInterface {
	return f.openAPIGetter
}

func (f *SingletonClients) KubeClient() *kube.Client {
	return f.helmClient
}

func (f *SingletonClients) Install() *action.Install {
	return f.install
}

func (f *SingletonClients) ReadyChecker(
	log func(string, ...interface{}), opts ...kube.ReadyCheckerOption,
) kube.ReadyChecker {
	return kube.NewReadyChecker(f.kubernetesClient, log, opts...)
}

func setKubernetesDefaults(config *rest.Config) error {
	// TODO remove this hack.  This is allowing the GetOptions to be serialized.
	config.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}

	if config.APIPath == "" {
		config.APIPath = "/api"
	}
	if config.NegotiatedSerializer == nil {
		// This codec factory ensures the resources are not converted. Therefore, resources
		// will not be round-tripped through internal versions. Defaulting does not happen
		// on the client.
		config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	}
	return rest.SetKubernetesDefaults(config)
}
