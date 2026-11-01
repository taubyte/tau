package protogen

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requirePOSIX skips fake-plugin tests on platforms where a shebang script
// isn't directly executable — the same POSIX assumption skipIfRoot already
// makes elsewhere in this package.
func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("fake plugin scripts need a POSIX shell + exec")
	}
}

// fakePlugin writes an executable shell script named name into dir that
// prints output on stdout and exits 0 — standing in for a real --version
// implementation without depending on any real toolchain being installed.
func fakePlugin(t *testing.T, dir, name, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := fmt.Sprintf("#!/bin/sh\necho %q\n", output)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeFailingPlugin writes an executable script that exits non-zero and
// prints nothing on stdout — a plugin binary that exists but chokes on
// --version (an unrecognized flag, say).
func fakeFailingPlugin(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\necho boom >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginVersion(t *testing.T) {
	requirePOSIX(t)
	dir := t.TempDir()

	// protoc-gen-go's own format: name + "v"-prefixed version.
	goBin := fakePlugin(t, dir, "fake-protoc-gen-go", "protoc-gen-go v1.36.11")
	got, err := pluginVersion(goBin)
	if err != nil || got != "1.36.11" {
		t.Fatalf("want (1.36.11, nil), got (%q, %v)", got, err)
	}

	// protoc-gen-connect-go's own format: bare version, no "v", no name.
	connectBin := fakePlugin(t, dir, "fake-protoc-gen-connect-go", "1.18.1")
	got, err = pluginVersion(connectBin)
	if err != nil || got != "1.18.1" {
		t.Fatalf("want (1.18.1, nil), got (%q, %v)", got, err)
	}

	emptyBin := fakePlugin(t, dir, "fake-empty", "")
	if _, err := pluginVersion(emptyBin); err == nil || !strings.Contains(err.Error(), "empty --version output") {
		t.Fatalf("want an 'empty --version output' error, got %v", err)
	}

	failBin := fakeFailingPlugin(t, dir, "fake-failing")
	if _, err := pluginVersion(failBin); err == nil {
		t.Fatal("want an error when the plugin exits non-zero")
	}

	if _, err := pluginVersion(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatal("want an error when the binary does not exist")
	}
}

func TestModulePlugins(t *testing.T) {
	dir := t.TempDir()

	if _, err := modulePlugins(dir); err == nil || !strings.Contains(err.Error(), "read buf.gen.yaml") {
		t.Fatalf("want a 'read buf.gen.yaml' error when the file is missing, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte("not: [valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := modulePlugins(dir); err == nil || !strings.Contains(err.Error(), "parse buf.gen.yaml") {
		t.Fatalf("want a 'parse buf.gen.yaml' error for malformed yaml, got %v", err)
	}

	cfg := `version: v2
plugins:
  - local: protoc-gen-go
    out: gen
  - local: protoc-gen-connect-go
    out: gen
`
	if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	used, err := modulePlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !used["protoc-gen-go"] || !used["protoc-gen-connect-go"] || len(used) != 2 {
		t.Fatalf("want exactly {protoc-gen-go, protoc-gen-connect-go}, got %v", used)
	}
}

// withGoMod writes a minimal go.mod at root requiring the given modules.
func withGoMod(t *testing.T, root string, requires map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("module fake\n\ngo 1.26\n\nrequire (\n")
	for mod, version := range requires {
		fmt.Fprintf(&b, "\t%s v%s\n", mod, version)
	}
	b.WriteString(")\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withBufGenYAML writes a buf.gen.yaml at absDir declaring the given local
// plugins — just enough for modulePlugins to read.
func withBufGenYAML(t *testing.T, absDir string, plugins ...string) {
	t.Helper()
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("version: v2\nplugins:\n")
	for _, p := range plugins {
		fmt.Fprintf(&b, "  - local: %s\n    out: gen\n", p)
	}
	if err := os.WriteFile(filepath.Join(absDir, "buf.gen.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPluginVersions_Match(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go", "protoc-gen-connect-go")
	withGoMod(t, root, map[string]string{
		"google.golang.org/protobuf": "1.36.11",
		"connectrpc.com/connect":     "1.18.1",
	})

	bin := t.TempDir()
	fakePlugin(t, bin, "protoc-gen-go", "protoc-gen-go v1.36.11")
	fakePlugin(t, bin, "protoc-gen-connect-go", "1.18.1")
	t.Setenv("PATH", bin)

	if err := checkPluginVersions(root, abs); err != nil {
		t.Fatalf("want a matching pin to pass, got %v", err)
	}
}

func TestCheckPluginVersions_UnrelatedPluginsSkipEntirely(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	// Only a plugin pluginPins knows nothing about — no go.mod needed at
	// all, and none was written, proving the short-circuit rather than a
	// lookup that happens to find nothing.
	withBufGenYAML(t, abs, "protoc-gen-es")

	if err := checkPluginVersions(root, abs); err != nil {
		t.Fatalf("want a module using none of pluginPins's plugins to pass trivially, got %v", err)
	}
}

func TestCheckPluginVersions_VersionMismatch(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	withGoMod(t, root, map[string]string{"google.golang.org/protobuf": "1.36.11"})

	bin := t.TempDir()
	fakePlugin(t, bin, "protoc-gen-go", "protoc-gen-go v1.30.0")
	t.Setenv("PATH", bin)

	err := checkPluginVersions(root, abs)
	if err == nil {
		t.Fatal("want an error on a version mismatch")
	}
	for _, want := range []string{"reports v1.30.0", "requires google.golang.org/protobuf v1.36.11"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("want error to contain %q, got: %v", want, err)
		}
	}
}

func TestCheckPluginVersions_PluginNotOnPath(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	withGoMod(t, root, map[string]string{"google.golang.org/protobuf": "1.36.11"})
	t.Setenv("PATH", t.TempDir()) // empty: nothing resolves

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("want a 'not found on PATH' error, got %v", err)
	}
}

func TestCheckPluginVersions_GoModDoesNotRequireModule(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	withGoMod(t, root, map[string]string{"example.com/unrelated": "1.0.0"})

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "no longer requires") {
		t.Fatalf("want a 'no longer requires' error, got %v", err)
	}
}

func TestCheckPluginVersions_GoModMissing(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	// no go.mod written at all

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "read go.mod") {
		t.Fatalf("want a 'read go.mod' error, got %v", err)
	}
}

func TestCheckPluginVersions_BufGenYAMLUnreadable(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	// No buf.gen.yaml at all: modulePlugins' own error must propagate
	// through checkPluginVersions unchanged, not get swallowed.

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "read buf.gen.yaml") {
		t.Fatalf("want modulePlugins's error to propagate, got %v", err)
	}
}

func TestCheckPluginVersions_PluginVersionCommandFails(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	withGoMod(t, root, map[string]string{"google.golang.org/protobuf": "1.36.11"})

	bin := t.TempDir()
	fakeFailingPlugin(t, bin, "protoc-gen-go")
	t.Setenv("PATH", bin)

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("want pluginVersion's failure to propagate, got %v", err)
	}
}

func TestCheckPluginVersions_GoModMalformed(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "mod")
	withBufGenYAML(t, abs, "protoc-gen-go")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("this is not a go.mod"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkPluginVersions(root, abs)
	if err == nil || !strings.Contains(err.Error(), "parse go.mod") {
		t.Fatalf("want a 'parse go.mod' error, got %v", err)
	}
}

func TestCheck_PluginVersionMismatchSurfacesThroughCheck(t *testing.T) {
	requirePOSIX(t)
	requireBuf(t)
	root, dir := newModule(t)

	bin := t.TempDir()
	fakePlugin(t, bin, "protoc-gen-go", "protoc-gen-go v0.0.1")
	realBuf, err := lookBuf()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBuf, filepath.Join(bin, "buf")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	_, err = Check(root, dir)
	if err == nil {
		t.Fatal("want Check to fail when the plugin on PATH doesn't match go.mod's pin")
	}
	if !strings.Contains(err.Error(), "plugin version check") {
		t.Fatalf("want the error to name the plugin version check step, got: %v", err)
	}
}
