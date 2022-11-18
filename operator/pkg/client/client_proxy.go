package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const restMappingErr = "client proxy failed to get resource mapping for"

func NewRuntimeClient(config *rest.Config, mapper meta.RESTMapper) (client.Client, error) {
	return client.New(config, client.Options{Mapper: mapper})
}

// Scheme returns the scheme this client is using.
func (s *SingletonClients) Scheme() *runtime.Scheme {
	return s.runtimeClient.Scheme()
}

// RESTMapper returns the rest mapper this client is using.
func (s *SingletonClients) RESTMapper() meta.RESTMapper {
	return s.runtimeClient.RESTMapper()
}

// Create implements client.Client.
func (s *SingletonClients) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj)
	}
	return s.runtimeClient.Create(ctx, obj, opts...)
}

// Update implements client.Client.
func (s *SingletonClients) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj)
	}
	return s.runtimeClient.Update(ctx, obj, opts...)
}

// Delete implements client.Client.
func (s *SingletonClients) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj)
	}
	return s.runtimeClient.Delete(ctx, obj, opts...)
}

// DeleteAllOf implements client.Client.
func (s *SingletonClients) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption,
) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj.GetResourceVersion())
	}
	return s.runtimeClient.DeleteAllOf(ctx, obj, opts...)
}

// Patch implements client.Client.
func (s *SingletonClients) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption,
) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj.GetResourceVersion())
	}
	return s.runtimeClient.Patch(ctx, obj, patch, opts...)
}

// Get implements client.Client.
func (s *SingletonClients) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj.GetResourceVersion())
	}
	return s.runtimeClient.Get(ctx, key, obj)
}

// List implements client.Client.
func (s *SingletonClients) List(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	if _, err := s.checkAndResetMapper(obj); err != nil {
		return fmt.Errorf("%s %v", restMappingErr, obj.GetResourceVersion())
	}
	return s.runtimeClient.List(ctx, obj, opts...)
}

// Status implements client.StatusClient.
func (s *SingletonClients) Status() client.StatusWriter {
	return s.runtimeClient.Status()
}
