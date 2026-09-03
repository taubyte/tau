// Package hermetic is where the forge tree's source-reading tests get their
// environment.
//
// Three tests here read their own package's source rather than trusting a
// comment about it: the ledger's population, and each driver's "this driver
// builds an HTTP request in exactly one place". Each one wrote its own
// packages.Config and only one of the three set an environment.
//
// MEASURED. Test binaries built first, then run with GOWORK naming a file that
// is not a workspace — a workspace somebody happens to have is not a
// hypothetical on a machine that develops two modules at once. The github and
// gitlab route tests both die on the load; the ledger's own load, which set
// GOWORK=off, does not notice. A test that reads source is only as trustworthy
// as the source it was handed, and a load that answers with somebody else's
// tree, or reaches the network for a module, is a closure over the wrong
// program.
//
// AN ENVIRONMENT IS A PROPERTY OF A PROCESS, NOT OF A CALL SITE, and that is
// why the fix is not three configs written the same way. Pin does it once for
// the whole test binary, so a load site nobody routed through Load is hermetic
// anyway — including one somebody adds tomorrow, and including the two that
// never set anything. Load is the same two settings written where a caller
// cannot decline them: appended AFTER whatever Env a config arrives with,
// because appended last is what wins.
//
// The ledger next door reads this tree's source for calls to packages.Load and
// names every one that is neither the call below nor in a binary this package
// has pinned.
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
