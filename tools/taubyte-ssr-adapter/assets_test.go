package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAssetModule(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<h1>home</h1>")
	write("about.html", "<h1>about</h1>") // flat prerender -> also /about/index.html
	write("robots.txt", "User-agent: *")
	write("_worker.js", "excluded host file")
	write("favicon.png", "PNGDATA")            // binary -> not embedded
	write("big.html", strings.Repeat("x", 50)) // over the tiny cap below -> not embedded

	src, n, err := buildAssetModule(dir, 32)
	if err != nil {
		t.Fatal(err)
	}

	mustHave := []string{
		`A["/index.html"]=`,
		`A["/about.html"]=`,
		`A["/about/index.html"]=`, // clean-URL form
		`A["/robots.txt"]=`,
	}
	for _, frag := range mustHave {
		if !strings.Contains(src, frag) {
			t.Errorf("asset module missing %q\n%s", frag, src)
		}
	}
	mustNotHave := []string{
		`A["/_worker.js"]`,  // host control file
		`A["/favicon.png"]`, // binary
		`A["/big.html"]`,    // over cap
	}
	for _, frag := range mustNotHave {
		if strings.Contains(src, frag) {
			t.Errorf("asset module unexpectedly contains %q", frag)
		}
	}
	// index + about(flat) + about(clean) + robots = 4 entries.
	if n != 4 {
		t.Errorf("embedded count = %d, want 4", n)
	}
	// Must install onto the shared global the shim reads.
	if !strings.Contains(src, "__TAUBYTE_ASSETS__") {
		t.Error("asset module must populate globalThis.__TAUBYTE_ASSETS__")
	}
}
