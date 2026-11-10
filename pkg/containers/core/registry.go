package core

// BackendFactory is a function that creates a backend from a config
type BackendFactory func(BackendConfig) (Backend, error)
