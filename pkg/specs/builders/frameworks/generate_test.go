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
	if !strings.Contains(script, websiteSpec.DefaultHandlerPath) {
		t.Errorf("ssr script missing handler output path:\n%s", script)
	}
	if !strings.Contains(script, websiteSpec.ManifestPath) {
		t.Errorf("ssr script missing manifest path:\n%s", script)
	}
	// The embedded manifest (between the heredoc markers) must be parseable.
	const marker = "TAUBYTE_SSR_EOF"
	open := strings.Index(script, marker+"'\n")
	if open < 0 {
		t.Fatalf("no manifest heredoc found in script:\n%s", script)
	}
	body := script[open+len(marker)+2:]
	close := strings.Index(body, "\n"+marker)
	if close < 0 {
		t.Fatalf("unterminated manifest heredoc in script:\n%s", script)
	}
	if _, err := websiteSpec.ParseManifest([]byte(body[:close])); err != nil {
		t.Errorf("embedded manifest is invalid: %v", err)
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
