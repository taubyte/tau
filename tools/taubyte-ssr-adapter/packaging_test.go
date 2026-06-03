package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func TestBuildHandlerZip(t *testing.T) {
	wasm := []byte("\x00asm\x01\x00\x00\x00fake-module")

	zipBytes, err := buildHandlerZip(wasm)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}

	f, err := zr.Open(wasmSpec.WasmFile) // must be main.wasm
	if err != nil {
		t.Fatalf("handler zip missing %s: %v", wasmSpec.WasmFile, err)
	}
	defer f.Close()

	var got bytes.Buffer
	if _, err := got.ReadFrom(f); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), wasm) {
		t.Error("wasm bytes not preserved in handler zip")
	}
}

func TestBuildManifest(t *testing.T) {
	data, err := buildManifest("hono").Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Must round-trip through the spec parser the runtime uses.
	m, err := websiteSpec.ParseManifest(data)
	if err != nil {
		t.Fatalf("adapter manifest rejected by spec: %v", err)
	}

	if !m.IsSSR() {
		t.Error("expected ssr manifest")
	}
	if m.ABIOrDefault() != websiteSpec.ABIWasiStdio {
		t.Errorf("abi = %q, want %q", m.ABIOrDefault(), websiteSpec.ABIWasiStdio)
	}
	if m.Handler != websiteSpec.DefaultHandlerPath {
		t.Errorf("handler = %q", m.Handler)
	}
	if m.Classify("/api/x") != websiteSpec.RouteAPI {
		t.Error("expected /api/x -> api")
	}
}

func TestBuildManifestUnknownFramework(t *testing.T) {
	// An unknown framework must still produce a valid manifest (no static
	// prefixes, but otherwise well-formed).
	if _, err := buildManifest("totally-unknown").Marshal(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSiteZip(t *testing.T) {
	// Mimic an edge build output dir (SvelteKit's .svelte-kit/cloudflare).
	dir := t.TempDir()
	files := map[string]string{
		"index.html":                   "<h1>home</h1>", // prerendered /
		"404.html":                     "nope",
		"_app/immutable/chunks/app.js": "console.log(1)",
		"about.html":                   "<h1>about</h1>", // flat prerender (SvelteKit shape)
		"_worker.js":                   "should be excluded",
		"_routes.json":                 "{}",
		"_headers":                     "x",
		"cloudflare-tmp/manifest.js":   "internal",
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	handler := []byte("HANDLER-ZIP-BYTES")
	zipBytes, err := buildSiteZip(dir, handler, buildManifest("sveltekit"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		got[f.Name] = b.String()
	}

	// Static/prerendered assets must be served from the site root. A flat
	// prerender (about.html) must ALSO appear in clean-URL form (about/index.html)
	// so /about resolves through the static layer.
	for _, want := range []string{"index.html", "404.html", "_app/immutable/chunks/app.js", "about.html", "about/index.html"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected static asset %q in build zip", want)
		}
	}
	// Host control files must be excluded.
	for _, bad := range []string{"_worker.js", "_routes.json", "_headers", "cloudflare-tmp/manifest.js"} {
		if _, ok := got[bad]; ok {
			t.Errorf("control file %q must not be served as a static asset", bad)
		}
	}
	// Handler + manifest must be embedded under __taubyte__/.
	if got[websiteSpec.DefaultHandlerPath] != string(handler) {
		t.Errorf("handler bytes missing/incorrect at %q", websiteSpec.DefaultHandlerPath)
	}
	if _, ok := got[websiteSpec.ManifestPath]; !ok {
		t.Errorf("manifest missing at %q", websiteSpec.ManifestPath)
	}
	// The embedded manifest must still satisfy the runtime's parser.
	if _, err := websiteSpec.ParseManifest([]byte(got[websiteSpec.ManifestPath])); err != nil {
		t.Errorf("embedded manifest rejected by spec: %v", err)
	}
}
