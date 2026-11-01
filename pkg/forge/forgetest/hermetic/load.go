// Package hermetic is the environment for the forge tree's source-reading tests.
// GOWORK and GOPROXY are properties of a process, so Pin sets them once for the
// whole test binary, and Load appends them after whatever Env a config carries,
// where they win.
package hermetic

import (
	"os"
	"sync"

	"golang.org/x/tools/go/packages"
)

// GOPROXY=off makes a module that is not already in the module cache a loud
// failure rather than a network fetch from inside a test. GOWORK=off keeps a
// workspace from deciding which source is read.
var hermetically = []string{"GOPROXY=off", "GOWORK=off"}

var pinned sync.Once

// Pin puts them on the process, once, for every load in this binary however it
// is written. The conformance suite calls it from an init, so a driver's tests
// get it by running the suite at all.
func Pin() {
	pinned.Do(func() {
		for _, kv := range hermetically {
			os.Setenv(kv[:len(kv)-len("=off")], "off")
		}
	})
}

// Load type-checks packages under that environment whatever the caller's config
// says.
func Load(cfg *packages.Config, patterns ...string) ([]*packages.Package, error) {
	Pin()
	if cfg == nil {
		cfg = &packages.Config{}
	}
	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}
	cfg.Env = append(env, hermetically...)
	return packages.Load(cfg, patterns...)
}
