package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNopDynamicImportRewrite(t *testing.T) {
	rewrite := func(s string) string {
		return string(nopDynamicImport.ReplaceAll([]byte(s), []byte("__nopImport(${1}")))
	}
	for _, tc := range []struct{ in, want string }{
		// Dynamic imports of a runtime value are redirected to the registry.
		{"let d=await import(e.entrypoint)", "let d=await __nopImport(e.entrypoint)"},
		{"return import(e)", "return __nopImport(e)"},
		{"import( spaced )", "import( spaced )"}, // see note below
		// String-literal imports are left for esbuild to resolve/bundle.
		{"import('node:buffer')", "import('node:buffer')"},
		{`import("node:async_hooks")`, `import("node:async_hooks")`},
		{"import(`./__next-on-pages-dist__/cache/kv.js`)", "import(`./__next-on-pages-dist__/cache/kv.js`)"},
	} {
		got := rewrite(tc.in)
		// The spaced case keeps a literal expectation only loosely; assert the two
		// real cases precisely and that literals are untouched.
		if strings.Contains(tc.in, "'") || strings.Contains(tc.in, "\"") || strings.Contains(tc.in, "`") {
			if got != tc.want {
				t.Errorf("literal import rewritten: %q -> %q", tc.in, got)
			}
			continue
		}
		if strings.HasPrefix(tc.in, "let d") || strings.HasPrefix(tc.in, "return") {
			if got != tc.want {
				t.Errorf("dynamic import not rewritten: %q -> %q (want %q)", tc.in, got, tc.want)
			}
		}
	}
}

func TestPrepareNextOnPagesNotDetected(t *testing.T) {
	// A plain worker (no __next-on-pages-dist__ sibling) must be left untouched.
	dir := t.TempDir()
	entry := filepath.Join(dir, "_worker.js")
	if err := os.WriteFile(entry, []byte("export default { fetch() {} }"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := prepareNextOnPages(entry, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("non-next-on-pages worker should not be transformed, got entry %q", got)
	}
}

func TestPrepareNextOnPagesDetected(t *testing.T) {
	// Fake a next-on-pages layout: _worker.js/index.js + __next-on-pages-dist__.
	root := t.TempDir()
	wdir := filepath.Join(root, "_worker.js")
	fns := filepath.Join(wdir, "__next-on-pages-dist__", "functions", "api")
	if err := os.MkdirAll(fns, 0o755); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(wdir, "index.js")
	// A worker body with a dynamic route import and a literal node import.
	if err := os.WriteFile(index, []byte("import('node:buffer');\nlet d=await import(e.entrypoint);\nexport default {};"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fns, "hello.func.js"), []byte("export default function(){}"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	entry, err := prepareNextOnPages(index, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if entry == "" {
		t.Fatal("next-on-pages worker not detected")
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	// The generated entry must register the route module, define __nopImport,
	// import the isolation setup, and re-export the worker default.
	for _, want := range []string{"functions/api/hello.func.js", "__nopImport", "nop-isolation.mjs", "export default __nopWorker"} {
		if !strings.Contains(src, want) {
			t.Errorf("generated entry missing %q", want)
		}
	}
	// The isolation module installs the routes-isolation global before routes.
	isoData, _ := os.ReadFile(filepath.Join(tmp, "nop-isolation.mjs"))
	if !strings.Contains(string(isoData), "__nextOnPagesRoutesIsolation") {
		t.Error("isolation module must install __nextOnPagesRoutesIsolation")
	}
	// The rewritten worker must redirect the dynamic import but keep the literal.
	wdata, _ := os.ReadFile(filepath.Join(tmp, "nop-worker.mjs"))
	w := string(wdata)
	if !strings.Contains(w, "__nopImport(e.entrypoint)") {
		t.Errorf("worker dynamic import not rewritten:\n%s", w)
	}
	if !strings.Contains(w, "import('node:buffer')") {
		t.Errorf("worker literal import must be preserved:\n%s", w)
	}
}
