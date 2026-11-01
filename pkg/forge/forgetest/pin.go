package forgetest

import "github.com/taubyte/tau/pkg/forge/forgetest/hermetic"

// Every binary running this suite reads source under hermetic's environment,
// set once for the process.
func init() { hermetic.Pin() }
