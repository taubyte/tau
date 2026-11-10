package containers

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/containers/core"
)

// choose attempts to create and health-check backends in order of preference.
// Returns the first available backend, preferring Docker over containerd.
// Returns an error if neither backend is available.
func (f factories) choose() (core.Backend, error) {
	ctx := context.Background()
	availableBackends := f.available()

	dockerRegistered := false
	for _, bt := range availableBackends {
		if bt == core.BackendTypeDocker {
			dockerRegistered = true
			break
		}
	}

	if dockerRegistered {
		dockerConfig := core.DockerConfig{}
		dockerBackend, err := f.build(dockerConfig)
		if err == nil {
			if err := dockerBackend.HealthCheck(ctx); err == nil {
				return dockerBackend, nil
			}
		}
	}

	containerdRegistered := false
	for _, bt := range availableBackends {
		if bt == core.BackendTypeContainerd {
			containerdRegistered = true
			break
		}
	}

	if containerdRegistered {
		containerdConfig := core.ContainerdConfig{}
		containerdBackend, err := f.build(containerdConfig)
		if err == nil {
			if err := containerdBackend.HealthCheck(ctx); err == nil {
				return containerdBackend, nil
			}
		}
	}

	if len(availableBackends) == 0 {
		return nil, fmt.Errorf("no backends to choose from")
	}

	var errors []string
	if dockerRegistered {
		errors = append(errors, "docker health check failed")
	}
	if containerdRegistered {
		errors = append(errors, "containerd health check failed")
	}
	if len(errors) == 0 {
		return nil, fmt.Errorf("no registered backends available (docker and containerd not registered)")
	}

	return nil, fmt.Errorf("no available backend: %s", strings.Join(errors, ", "))
}

// defaultBackend is the backend chosen for a client given none: docker first, then
// containerd if docker is unavailable.
func (c *Client) defaultBackend() (core.Backend, error) {
	f := c.factories
	if f == nil {
		f = carried
	}
	return f.choose()
}
