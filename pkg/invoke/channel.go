package invoke

import "github.com/taubyte/tau/pkg/specs"

// SettingServeRuntime is where the composer writes the tier a channel's server runs
// on, which decides how a dial reaches it.
const SettingServeRuntime = "serve/runtime"

// Connects reports whether a channel grant's server takes the bytes on a socket: a
// process on the container tier listens, anything else is dispatched.
func (g Grant) Connects() bool {
	return g.Settings[SettingServeRuntime] == specs.PackageTypeOci
}
