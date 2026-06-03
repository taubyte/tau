package nextjs

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func zipEntries(t *testing.T, p string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b [4096]byte
		n, _ := rc.Read(b[:])
		rc.Close()
		out[f.Name] = string(b[:n])
	}
	return out
}

func TestAssembleStaticOnly(t *testing.T) {
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json":      `{"version":3,"basePath":"","staticRoutes":[{"page":"/"},{"page":"/about"}],"dynamicRoutes":[]}`,
		".next/static/chunks/main-abc.js": "console.log(1)",
		".next/server/app/about.html":     "<html>about</html>",
		".next/server/app/index.html":     "<html>home</html>",
		"public/favicon.ico":              "icon",
	})

	outZip := filepath.Join(t.TempDir(), "build.zip")
	rep, err := Assemble(AssembleOptions{ProjectDir: dir, Out: outZip})
	if err != nil {
		t.Fatal(err)
	}
	if rep.HandlerEmbedded() {
		t.Error("no handler was provided; should not be embedded")
	}

	entries := zipEntries(t, outZip)
	for _, want := range []string{
		"_next/static/chunks/main-abc.js",
		"favicon.ico",
		"about.html",
		"index.html",
		"about/index.html", // clean-URL form so /about resolves via dir index
	} {
		if _, ok := entries[want]; !ok {
			t.Errorf("missing asset entry %q (have %v)", want, keys(entries))
		}
	}
	// No handler => no manifest (pure static asset).
	if _, ok := entries[websiteSpec.ManifestPath]; ok {
		t.Error("static-only asset should not contain an SSR manifest")
	}
}

func TestAssembleWithHandler(t *testing.T) {
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json":  `{"version":3,"basePath":"","staticRoutes":[{"page":"/"}],"dynamicRoutes":[{"page":"/blog/[slug]"}]}`,
		".next/static/chunks/main.js": "x",
	})

	handler := filepath.Join(t.TempDir(), "handler.wasm.zip")
	if err := os.WriteFile(handler, []byte("FAKE-WASM-ZIP"), 0o644); err != nil {
		t.Fatal(err)
	}

	outZip := filepath.Join(t.TempDir(), "build.zip")
	rep, err := Assemble(AssembleOptions{ProjectDir: dir, Out: outZip, HandlerZip: handler})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.HandlerEmbedded() {
		t.Error("handler should be embedded")
	}

	entries := zipEntries(t, outZip)
	if entries[websiteSpec.DefaultHandlerPath] != "FAKE-WASM-ZIP" {
		t.Errorf("handler not embedded correctly: %q", entries[websiteSpec.DefaultHandlerPath])
	}
	manifestJSON, ok := entries[websiteSpec.ManifestPath]
	if !ok {
		t.Fatal("manifest missing")
	}
	// The embedded manifest must be a valid wasi-stdio SSR manifest.
	m, err := websiteSpec.ParseManifest([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("embedded manifest invalid: %v", err)
	}
	if m.ABIOrDefault() != websiteSpec.ABIWasiStdio || !m.IsSSR() {
		t.Errorf("unexpected manifest: abi=%s ssr=%v", m.ABIOrDefault(), m.IsSSR())
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
