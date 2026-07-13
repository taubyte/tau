package compile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Guards the cache decision: stable <base>.zwasm name, and freshness by mtime —
// serve when the asset is no older than the source, rebuild when the source is
// newer or a file in a source directory is touched.
func TestCachePathAndFreshness(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "echo.go")
	if err := os.WriteFile(src, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := resourceContext{paths: []string{src}}

	got := ctx.cachePath()
	want := filepath.Join(dir, "echo.zwasm")
	if got != want {
		t.Fatalf("cachePath = %q, want %q", got, want)
	}

	// No asset yet → not fresh.
	if cacheFresh(got, ctx.paths) {
		t.Fatal("cacheFresh true with no asset")
	}

	// Asset newer than source → fresh.
	writeCache(got, []byte("built"))
	if err := os.Chtimes(src, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !cacheFresh(got, ctx.paths) {
		t.Fatal("cacheFresh false when asset is newer than source")
	}
	if b, _ := os.ReadFile(got); string(b) != "built" {
		t.Fatalf("asset content = %q, want built", b)
	}

	// Touch source after the asset → stale.
	if err := os.Chtimes(src, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if cacheFresh(got, ctx.paths) {
		t.Fatal("cacheFresh true when source is newer than asset")
	}
}

// A touched file inside a source directory must make the asset stale.
func TestCacheFreshnessDirectory(t *testing.T) {
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
	asset := ctx.cachePath()
	if asset != filepath.Join(dir, "lib.zwasm") {
		t.Fatalf("cachePath = %q, want lib.zwasm next to the dir", asset)
	}

	writeCache(asset, []byte("built"))
	if err := os.Chtimes(srcDir, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.go", "b.go"} {
		os.Chtimes(filepath.Join(srcDir, f), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	}
	if !cacheFresh(asset, ctx.paths) {
		t.Fatal("cacheFresh false when all dir files are older than asset")
	}

	// Touch one file → stale.
	if err := os.Chtimes(filepath.Join(srcDir, "b.go"), time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if cacheFresh(asset, ctx.paths) {
		t.Fatal("cacheFresh true after a dir file was touched")
	}
}
