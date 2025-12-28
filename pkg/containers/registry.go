package containers

// BackendFactory is a generic factory function for creating backends
type BackendFactory[T BackendConfig] func(T) (Backend, error)

var backends = make(map[BackendType]interface{})

// RegisterBackend registers a backend factory function with its config type
func RegisterBackend[T BackendConfig](backendType BackendType, factory BackendFactory[T]) {
	backends[backendType] = factory
}

// AvailableBackends returns all registered backends
func AvailableBackends() []BackendType {
	var available []BackendType
	for backendType := range backends {
		available = append(available, backendType)
	}
	return available
}

// NewBackend creates a backend instance using the registered factory
func NewBackend(config BackendConfig) (Backend, error) {
	factory, exists := backends[config.BackendType()]
	if !exists {
		return nil, ErrBackendNotAvailable
	}

	// Type-assert based on backend type
	switch config.BackendType() {
	case BackendTypeContainerd:
		if f, ok := factory.(BackendFactory[ContainerdConfig]); ok {
			return f(config.(ContainerdConfig))
		}
	case BackendTypeFirecracker:
		if f, ok := factory.(BackendFactory[FirecrackerConfig]); ok {
			return f(config.(FirecrackerConfig))
		}
	case BackendTypeNanos:
		if f, ok := factory.(BackendFactory[NanosConfig]); ok {
			return f(config.(NanosConfig))
		}
	}

	return nil, ErrBackendNotAvailable
}
