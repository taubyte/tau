package containers

import (
	"github.com/taubyte/tau/pkg/containers/core"
)

// AvailableBackends returns all registered backends
func AvailableBackends() []core.BackendType {
	return core.AvailableBackendTypes()
}

// NewBackend creates a backend instance using the registered factory from core
func NewBackend(config core.BackendConfig) (core.Backend, error) {
	factory, exists := core.GetBackendFactory(config.BackendType())
	if !exists {
		return nil, ErrBackendNotAvailable
	}
	return factory(config)
}
