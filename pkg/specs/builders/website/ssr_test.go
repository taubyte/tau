package website

import (
	"os"
	"path/filepath"
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func TestIsSSROutput(t *testing.T) {
	dir := t.TempDir()

	if IsSSROutput(dir) {
		t.Error("empty output should not be SSR")
	}

	manifestPath := SSRManifestPath(dir)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"render":"ssr"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if !IsSSROutput(dir) {
		t.Error("output with manifest should be SSR")
	}

	if filepath.Base(SSRHandlerPath(dir)) != "handler.wasm.zip" {
		t.Errorf("unexpected handler path %q", SSRHandlerPath(dir))
	}
	// The manifest path must agree with the spec's well-known location.
	if SSRManifestPath(dir) != filepath.Join(dir, filepath.FromSlash(websiteSpec.ManifestPath)) {
		t.Error("manifest path mismatch with spec")
	}
}
