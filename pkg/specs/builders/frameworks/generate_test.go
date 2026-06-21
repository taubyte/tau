package frameworks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonSpec "github.com/taubyte/tau/pkg/specs/builders/common"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func TestGenerateStatic(t *testing.T) {
	vite, _ := Get("vite")
	g, err := Generate(vite)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(g.Config, "image: node:20-alpine") {
		t.Errorf("config missing image:\n%s", g.Config)
	}
	if !strings.Contains(g.Config, "- build") {
		t.Errorf("config missing workflow:\n%s", g.Config)
	}

	script := g.Scripts["build.sh"]
	if !strings.Contains(script, "npm run build") {
		t.Errorf("script missing build command:\n%s", script)
	}
	if !strings.Contains(script, "'dist'/. \"$OUT\"") {
		t.Errorf("script missing static publish:\n%s", script)
	}
	if g.Manifest.IsSSR() {
		t.Error("vite manifest should not be ssr")
	}
}

func TestGenerateSSR(t *testing.T) {
	next, _ := Get("nextjs")
	g, err := Generate(next)
	if err != nil {
		t.Fatal(err)
	}

	script := g.Scripts["build.sh"]
	if !strings.Contains(script, "TAUBYTE_SSR_ADAPTER") {
		t.Errorf("ssr script missing adapter invocation:\n%s", script)
	}
	if !strings.Contains(script, "--out \"$OUT/"+websiteSpec.DefaultHandlerPath) {
		t.Errorf("ssr script missing handler output path:\n%s", script)
	}
	if !strings.Contains(script, "--manifest \"$OUT/"+websiteSpec.ManifestPath) {
		t.Errorf("ssr script missing manifest output path:\n%s", script)
	}
	// The framework's manifest must be a valid wasi-stdio SSR manifest.
	if g.Manifest.ABIOrDefault() != websiteSpec.ABIWasiStdio {
		t.Errorf("ssr manifest abi = %q, want %q", g.Manifest.ABIOrDefault(), websiteSpec.ABIWasiStdio)
	}
	data, err := g.Manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := websiteSpec.ParseManifest(data); err != nil {
		t.Errorf("framework manifest is invalid: %v", err)
	}
}

func TestMaterialize(t *testing.T) {
	dir := t.TempDir()
	vite, _ := Get("vite")
	g, _ := Generate(vite)

	if err := Materialize(dir, g); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, commonSpec.TaubyteDir, commonSpec.ConfigFile)
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, commonSpec.TaubyteDir, "build.sh")); err != nil {
		t.Fatalf("build script not written: %v", err)
	}

	// Must refuse to clobber an existing config.
	if err := Materialize(dir, g); err == nil {
		t.Error("expected Materialize to refuse overwriting existing config")
	}
}
