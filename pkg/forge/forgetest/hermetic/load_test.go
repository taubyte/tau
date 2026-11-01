package hermetic_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/forge/forgetest/hermetic"
)

func TestThePinIsOnTheProcess(t *testing.T) {
	hermetic.Pin()
	for _, name := range []string{"GOPROXY", "GOWORK"} {
		if got := os.Getenv(name); got != "off" {
			t.Errorf("this binary's %s is %q and not \"off\", so a source load in it that writes no environment of its own reads under whatever the machine running the test happens to have — which is what every load site the ledger excuses as pinned is relying on", name, got)
		}
	}
}

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
