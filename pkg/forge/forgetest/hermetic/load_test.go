package hermetic_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/v2/forge/forgetest/hermetic"
)

// TestThePinIsOnTheProcess is Pin's own fault. The ledger next door excuses a
// load site in a binary this package has pinned — which is what holds the two
// drivers' route tables, neither of which routes its load through anything —
// and it reads that exemption out of source: a package calling Pin from an
// init. Source says the call is there; only this says the call did anything.
//
// The process environment IS the mechanism, so it is read back. Every
// packages.Load in this binary that builds no Env of its own reads exactly
// these two, and with Pin's body emptied they read whatever the machine has.
func TestThePinIsOnTheProcess(t *testing.T) {
	hermetic.Pin()
	for _, name := range []string{"GOPROXY", "GOWORK"} {
		if got := os.Getenv(name); got != "off" {
			t.Errorf("this binary's %s is %q and not \"off\", so a source load in it that writes no environment of its own reads under whatever the machine running the test happens to have — which is what every load site the ledger excuses as pinned is relying on", name, got)
		}
	}
}

// TestTheEnvironmentIsNotSomethingACallerCanDecline is this loader's own fault,
// and it is what keeps the two lines that matter from being decoration.
//
// The ledger next door names every packages.Load in the forge tree that is not
// the one inside Load. That closes the SITES and says nothing about what Load
// itself does, so deleting the append would put every one of them back under
// whatever environment the machine happens to have, with the closure still
// green and still counting three sites as one.
//
// So the loader is handed a hostile environment and required to survive it: a
// GOWORK naming a file that is not a workspace, which `go list` refuses to
// start under, and a GOPROXY pointing at a host that does not resolve. Both are
// written into the config AFTER os.Environ, which is where a caller's own
// setting would sit, and the load has to come back with the package anyway.
// With the append removed this fails on the first line of output.
func TestTheEnvironmentIsNotSomethingACallerCanDecline(t *testing.T) {
	hostile := filepath.Join(t.TempDir(), "somebody-elses.work")
	if err := os.WriteFile(hostile, []byte("this is not a workspace file\n"), 0o600); err != nil {
		t.Fatalf("writing the workspace this load must ignore: %v", err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName,
		Dir:  ".",
		Env: append(os.Environ(),
			"GOWORK="+hostile,
			"GOPROXY=https://a-proxy-that-does-not-resolve.invalid"),
	}
	pkgs, err := hermetic.Load(cfg, ".")
	if err != nil {
		t.Fatalf("this loader was handed a GOWORK naming a file that is not a workspace and it did not override it, so every source-reading test in this tree reads whatever tree the machine running it happens to be in: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "hermetic" {
		t.Fatalf("loading this package answered %d packages and not the one it is, so nothing above was measured against a load that worked", len(pkgs))
	}

	// AND THE OVERRIDE IS LAST, because appended last is what wins. A caller
	// that sets its own is not refused, it is overridden — which is the whole
	// of "not something a caller can decline".
	tail := cfg.Env[len(cfg.Env)-2:]
	if tail[0] != "GOPROXY=off" || tail[1] != "GOWORK=off" {
		t.Errorf("the config this loader handed to packages.Load ends %v, so a caller's own GOPROXY or GOWORK could sit after the override and decide what is read", tail)
	}
}

// TestAConfigThatBringsNoEnvironmentOfItsOwn is the ordinary case and the
// degenerate one. The ledger's own load writes no Env, and a caller that writes
// no config at all is the same question one step further: both have to end up
// reading the process environment with the override after it, not instead of
// it.
func TestAConfigThatBringsNoEnvironmentOfItsOwn(t *testing.T) {
	for name, cfg := range map[string]*packages.Config{
		"a config carrying no Env": {Mode: packages.NeedName, Dir: "."},
		"no config at all":         nil,
	} {
		t.Run(name, func(t *testing.T) {
			pkgs, err := hermetic.Load(cfg, ".")
			if err != nil {
				t.Fatalf("loading with %s: %v", name, err)
			}
			if len(pkgs) != 1 || pkgs[0].Name != "hermetic" {
				t.Fatalf("loading with %s answered %d packages and not the one it is", name, len(pkgs))
			}
		})
	}
}
