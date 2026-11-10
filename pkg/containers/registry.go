package containers

import (
	"slices"

	"github.com/taubyte/tau/pkg/containers/core"
)

// factories is the backends a choice is made from, by type.
type factories map[core.BackendType]core.BackendFactory

// AvailableBackends returns every backend this build carries.
func AvailableBackends() []core.BackendType {
	return carried.available()
}

// NewBackend creates a backend of the config's type.
func NewBackend(config core.BackendConfig) (core.Backend, error) {
	return carried.build(config)
}

func (f factories) available() []core.BackendType {
	types := make([]core.BackendType, 0, len(f))
	for t := range f {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

func (f factories) build(config core.BackendConfig) (core.Backend, error) {
	factory, exists := f[config.BackendType()]
	if !exists {
		return nil, ErrBackendNotAvailable
	}
	return factory(config)
}
