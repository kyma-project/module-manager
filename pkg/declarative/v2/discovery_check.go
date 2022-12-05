package v2

import (
	"errors"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

var ErrResourcesNotReady = errors.New("groups and resources not ready")

type DiscoveryCheck struct {
	ResourceChecks
	AllowError bool
}

func NewDiscoveryCheck(checks ...ResourceCheck) *DiscoveryCheck {
	return &DiscoveryCheck{ResourceChecks: checks}
}

type (
	ResourceCheck  func(*v1.APIResourceList) bool
	ResourceChecks []ResourceCheck
)

func (c *DiscoveryCheck) RunAgainstCluster(config *rest.Config) error {
	disco, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return err
	}

	// The returned group and resource lists might be non-nil with partial results
	// even in the case of non-nil error.
	_, resources, err := disco.ServerGroupsAndResources()
	if err != nil && !c.AllowError {
		return err
	}

	for _, resource := range resources {
		for _, check := range c.ResourceChecks {
			if check(resource) {
				return nil
			}
		}
	}

	return ErrResourcesNotReady
}
