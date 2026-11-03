package js

import (
	"github.com/samyfodil/warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
)

// WithRealm installs more into each realm before its bundle runs, so a test
// can give a handler a host-async call to wait on.
func WithRealm(f func(*engine.Runtime) error) Option {
	return func(r *Runtime) { r.realm = f }
}

// Realm is the engine an instance runs, so a benchmark can call into it
// without the tier around the call.
func Realm(inst invoke.Instance) *engine.Runtime { return inst.(*instance).rt }
