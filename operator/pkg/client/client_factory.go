package client

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/module-manager/operator/pkg/types"
	"github.com/kyma-project/module-manager/operator/pkg/util"
)

const (
	apis = "/apis"
	api  = "/api"
)

// SingletonClients serves as a single-minded client interface that combines
// all kubernetes Client APIs (Helm, Kustomize, Kubernetes, Client-Go) under the hood.
// It offers a simple initialization lifecycle during creation, but delegates all
// heavy-duty work to deferred discovery logic and a single http client
// as well as a client cache to support GV-based clients.
type SingletonClients struct {
	httpClient *http.Client

	// controller runtime client
	runtimeClient client.Client

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

// Since we use the SingletonClients also as our Helm cients and redirect all Helm calls to this,
// we check interface compliance with the necessary client interfaces here.
var (
	_ kube.Factory            = &SingletonClients{}
	_ action.RESTClientGetter = &SingletonClients{}
	_ client.Client           = &SingletonClients{}
)

func NewSingletonClients(info types.ClusterInfo, logger logr.Logger) (*SingletonClients, error) {
	if err := setKubernetesDefaults(info.Config); err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(info.Config)
	if err != nil {
		return nil, err
	}

	discoveryConfig := *info.Config
	discoveryConfig.Burst = 200
	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(&discoveryConfig, httpClient)
	if err != nil {
		return nil, err
	}
	cachedDiscoveryClient := memory.NewMemCacheClient(discoveryClient)
	discoveryRESTMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscoveryClient)
	discoveryShortcutExpander := restmapper.NewShortcutExpander(discoveryRESTMapper, cachedDiscoveryClient)

	// create runtime client only if not passed
	runtimeClient := info.Client
	if info.Client == nil {
		runtimeClient, err = NewRuntimeClient(info.Config, discoveryShortcutExpander)
		if err != nil {
			return nil, err
		}
	}

	kubernetesClient, err := kubernetes.NewForConfigAndClient(info.Config, httpClient)
	if err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfigAndClient(info.Config, httpClient)
	if err != nil {
		return nil, err
	}

	openAPIGetter := openapi.NewOpenAPIGetter(cachedDiscoveryClient)

	clients := &SingletonClients{
		httpClient:                  httpClient,
		config:                      info.Config,
		discoveryClient:             cachedDiscoveryClient,
		discoveryRESTMapper:         discoveryRESTMapper,
		discoveryShortcutExpander:   discoveryShortcutExpander,
		kubernetesClient:            kubernetesClient,
		dynamicClient:               dynamicClient,
		openAPIGetter:               openAPIGetter,
		openAPIParser:               openapi.NewOpenAPIParser(openAPIGetter),
		structuredRestClientCache:   map[string]resource.RESTClient{},
		unstructuredRestClientCache: map[string]resource.RESTClient{},
		runtimeClient:               runtimeClient,
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

func (s *SingletonClients) ToRESTConfig() (*rest.Config, error) {
	return s.config, nil
}

func (s *SingletonClients) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return s.discoveryClient, nil
}

func (s *SingletonClients) ToRESTMapper() (meta.RESTMapper, error) {
	return s.discoveryShortcutExpander, nil
}

func (s *SingletonClients) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}

func (s *SingletonClients) KubernetesClientSet() (*kubernetes.Clientset, error) {
	return s.kubernetesClient, nil
}

func (s *SingletonClients) DynamicClient() (dynamic.Interface, error) {
	return s.dynamicClient, nil
}

// NewBuilder returns a new resource builder for structured api objects.
func (s *SingletonClients) NewBuilder() *resource.Builder {
	return resource.NewBuilder(s)
}

func (s *SingletonClients) RESTClient() (*rest.RESTClient, error) {
	return rest.RESTClientForConfigAndClient(s.config, s.httpClient)
}

func (s *SingletonClients) clientCacheKeyForMapping(mapping *meta.RESTMapping) string {
	return fmt.Sprintf("%s+%s:%s",
		mapping.Resource.String(), mapping.GroupVersionKind.String(), mapping.Scope.Name())
}

func (s *SingletonClients) ClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	s.structuredSyncLock.Lock()
	defer s.structuredSyncLock.Unlock()
	key := s.clientCacheKeyForMapping(mapping)
	client, found := s.structuredRestClientCache[key]

	if found {
		return client, nil
	}

	cfg := rest.CopyConfig(s.config)
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
	client, err = rest.RESTClientForConfigAndClient(cfg, s.httpClient)
	if err != nil {
		return nil, err
	}

	s.structuredRestClientCache[key] = client
	return client, err
}

func (s *SingletonClients) DynamicResourceInterface(obj *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	mapping, err := s.checkAndResetMapper(obj)
	if err != nil {
		return nil, err
	}
	gvk := obj.GroupVersionKind()

	var dynamicResource dynamic.ResourceInterface

	namespace := obj.GetNamespace()

	switch mapping.Scope.Name() {
	case meta.RESTScopeNameNamespace:
		if namespace == "" {
			return nil, fmt.Errorf("namespace was not provided for namespace-scoped object %v", gvk)
		}
		dynamicResource = s.dynamicClient.Resource(mapping.Resource).Namespace(namespace)
	case meta.RESTScopeNameRoot:
		if namespace != "" {
			// TODO: Differentiate between server-fixable vs client-fixable errors?
			return nil, fmt.Errorf(
				"namespace %q was provided for cluster-scoped object %v", obj.GetNamespace(), gvk)
		}
		dynamicResource = s.dynamicClient.Resource(mapping.Resource)
	default:
		// Internal error ... this is panic-level
		return nil, fmt.Errorf("unknown scope for gvk %s: %q", gvk, mapping.Scope.Name())
	}
	return dynamicResource, nil
}

func (s *SingletonClients) UnstructuredClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	s.unstructuredSyncLock.Lock()
	defer s.unstructuredSyncLock.Unlock()
	key := s.clientCacheKeyForMapping(mapping)
	client, found := s.unstructuredRestClientCache[key]

	if found {
		return client, nil
	}

	cfg := rest.CopyConfig(s.config)
	cfg.APIPath = apis
	if mapping.GroupVersionKind.Group == corev1.GroupName {
		cfg.APIPath = api
	}
	gv := mapping.GroupVersionKind.GroupVersion()
	cfg.ContentConfig = resource.UnstructuredPlusDefaultContentConfig()
	cfg.GroupVersion = &gv

	var err error
	client, err = rest.RESTClientForConfigAndClient(cfg, s.httpClient)
	if err != nil {
		return nil, err
	}
	s.structuredRestClientCache[key] = client
	return client, err
}

func (s *SingletonClients) ResourceInfo(obj *unstructured.Unstructured) (*resource.Info, error) {
	mapping, err := s.checkAndResetMapper(obj)
	if err != nil {
		return nil, err
	}
	info := &resource.Info{}
	clnt, err := s.ClientForMapping(mapping)
	if err != nil {
		return nil, err
	}

	info.Client = clnt
	info.Mapping = mapping
	info.Namespace = obj.GetNamespace()
	info.Name = obj.GetName()
	info.Object = obj
	info.ResourceVersion = obj.GetResourceVersion()
	return info, nil
}

func (s *SingletonClients) Validator(
	validationDirective string, verifier *resource.QueryParamVerifier,
) (validation.Schema, error) {
	if validationDirective == metav1.FieldValidationIgnore {
		return validation.NullSchema{}, nil
	}

	resources, err := s.OpenAPISchema()
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
func (s *SingletonClients) OpenAPISchema() (openapi.Resources, error) {
	return s.openAPIParser.Parse()
}

func (s *SingletonClients) OpenAPIGetter() discovery.OpenAPISchemaInterface {
	return s.openAPIGetter
}

func (s *SingletonClients) KubeClient() *kube.Client {
	return s.helmClient
}

func (s *SingletonClients) Install() *action.Install {
	return s.install
}

func (s *SingletonClients) ReadyChecker(
	log func(string, ...interface{}), opts ...kube.ReadyCheckerOption,
) kube.ReadyChecker {
	return kube.NewReadyChecker(s.kubernetesClient, log, opts...)
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

func (s *SingletonClients) checkAndResetMapper(obj runtime.Object) (*meta.RESTMapping, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	mapping, err := s.discoveryShortcutExpander.RESTMapping(gvk.GroupKind(), gvk.Version)
	if gvk.Empty() {
		return mapping, nil
	}
	if err != nil {
		if meta.IsNoMatchError(err) {
			s.discoveryRESTMapper.Reset()
			return nil, fmt.Errorf("resetting REST mapper to update resource mappings: %w", err)
		}
	}
	return mapping, err
}

func (s *SingletonClients) ToRuntimeClient() client.Client {
	return s.runtimeClient
}
