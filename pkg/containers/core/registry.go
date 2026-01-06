package core

// BackendFactory is a function that creates a backend from a config
type BackendFactory func(BackendConfig) (Backend, error)

var backends = make(map[BackendType]BackendFactory)

// RegisterBackend registers a backend factory
// This is called by backend init() functions
func RegisterBackend(backendType BackendType, factory BackendFactory) {
	backends[backendType] = factory
}

// GetBackendFactory returns the factory for a given backend type
func GetBackendFactory(backendType BackendType) (BackendFactory, bool) {
	factory, exists := backends[backendType]
	return factory, exists
}

// AvailableBackendTypes returns all registered backend types
func AvailableBackendTypes() []BackendType {
	var types []BackendType
	for t := range backends {
		types = append(types, t)
	}
	return types
}
