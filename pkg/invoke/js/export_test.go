package js

import "warpjs/engine"

// WithRealm installs more into each realm before its bundle runs, so a test
// can give a handler a host-async call to wait on.
func WithRealm(f func(*engine.Runtime) error) Option {
	return func(r *Runtime) { r.realm = f }
}
