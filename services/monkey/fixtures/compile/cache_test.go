package compile

import (
	"os"
	"path/filepath"
	"testing"
)

// Guards the money path: the asset name must be stable for identical sources,
// flip when any source byte changes, and writeCache must prune only the stale
// variant of the same source (it calls os.Remove on committed files).
func TestCachePathAndWrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "echo.go")
	if err := os.WriteFile(src, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := resourceContext{paths: []string{src}}

	p1, base, err := ctx.cachePath()
	if err != nil {
		t.Fatal(err)
	}
	if base != "echo" {
		t.Fatalf("base = %q, want echo", base)
	}
	if filepath.Ext(p1) != ".zwasm" || filepath.Dir(p1) != dir {
		t.Fatalf("unexpected cache path %q", p1)
	}
	if again, _, _ := ctx.cachePath(); again != p1 {
		t.Fatalf("cachePath not deterministic: %q vs %q", p1, again)
	}

	// Source change must produce a different asset name.
	if err := os.WriteFile(src, []byte("package main // v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, _, _ := ctx.cachePath()
	if p2 == p1 {
		t.Fatal("cachePath did not change after source edit")
	}

	// Writing p1 then p2 must leave only p2 (stale variant pruned).
	writeCache(p1, base, []byte("old"))
	writeCache(p2, base, []byte("new"))
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatalf("stale asset %q was not pruned", p1)
	}
	if got, _ := os.ReadFile(p2); string(got) != "new" {
		t.Fatalf("fresh asset content = %q, want new", got)
	}
}

// A change to any file inside a source directory must flip the asset name.
func TestCachePathDirectory(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "lib")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(srcDir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := resourceContext{paths: []string{srcDir}}
	before, base, err := ctx.cachePath()
	if err != nil {
		t.Fatal(err)
	}
	if base != "lib" {
		t.Fatalf("base = %q, want lib", base)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "b.go"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, _, _ := ctx.cachePath()
	if after == before {
		t.Fatal("cachePath did not change after a file in the dir changed")
	}
}
