package app

import (
	"context"
	"errors"
	"go/build"
	"slices"
	"testing"
)

// A config missing for a moment, as during a displace, must leave systemd free
// to restart the node: only a missing tenancy exits 78.
func TestStartParseErrorIsNotAConfigError(t *testing.T) {
	err := newApp().RunContext(context.Background(), []string{"tau", "start", "--shape", "absent", "--root", t.TempDir()})
	if err == nil {
		t.Fatal("start with no config file must fail")
	}
	if errors.As(err, new(ConfigError)) {
		t.Fatalf("a config parse failure must not exit 78: %v", err)
	}
}

// The ee module is private, so the EE variant is only checked for selection here;
// startable_ee_test.go proves its behaviour in EE builds.
func TestEEBuildRunsNoTenancyCheck(t *testing.T) {
	ctx := build.Default
	ctx.BuildTags = []string{"ee"}
	pkg, err := ctx.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pkg.GoFiles, "startable_ee.go") || slices.Contains(pkg.GoFiles, "startable.go") {
		t.Fatalf("EE build must compile startable_ee.go instead of startable.go: %v", pkg.GoFiles)
	}
}
