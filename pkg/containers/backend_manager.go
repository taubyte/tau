package containers

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/containers/core"
)

// Note: Backends must be imported elsewhere in the application to register themselves.
// For example:
//   import _ "github.com/taubyte/tau/pkg/containers/backends/docker"
//   import _ "github.com/taubyte/tau/pkg/containers/backends/containerd"

// selectBackend attempts to create and health-check backends in order of preference.
// Returns the first available backend, preferring Docker over containerd.
// Returns an error if neither backend is available.
func selectBackend() (core.Backend, error) {
	ctx := context.Background()
	availableBackends := AvailableBackends()

	dockerRegistered := false
	for _, bt := range availableBackends {
		if bt == core.BackendTypeDocker {
			dockerRegistered = true
			break
		}
	}

	if dockerRegistered {
		dockerConfig := core.DockerConfig{}
		dockerBackend, err := NewBackend(dockerConfig)
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
		containerdBackend, err := NewBackend(containerdConfig)
		if err == nil {
			if err := containerdBackend.HealthCheck(ctx); err == nil {
				return containerdBackend, nil
			}
		}
	}

	if len(availableBackends) == 0 {
		return nil, fmt.Errorf("no backends registered - import backends (e.g., _ \"github.com/taubyte/tau/pkg/containers/backends/docker\")")
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

// getDefaultBackend returns a backend instance using default configuration.
// It tries Docker first, then falls back to containerd if Docker is unavailable.
func getDefaultBackend() (core.Backend, error) {
	return selectBackend()
}
