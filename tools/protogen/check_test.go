package protogen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// skipIfRoot skips permission-based fault injection when running as root,
// where chmod 0 does not actually deny access.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("permission-based fault injection needs a POSIX filesystem")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not deny access")
	}
}

const testProto = `syntax = "proto3";

package testpkg.v1;

option go_package = ".";

message Ping {
    string value = 1;
}
`

const testBufGenYAML = `version: v2
plugins:
  - local: protoc-gen-go
    out: gen
    opt: paths=source_relative
`

// newModule lays out a minimal buf module (one .proto, a buf.gen.yaml with only
// protoc-gen-go) under repoRoot/protoDir, with a go.mod pinning
// google.golang.org/protobuf to the version the installed protoc-gen-go reports.
func newModule(t *testing.T) (repoRoot, protoDir string) {
	t.Helper()
	root := t.TempDir()
	dir := "modA"
	abs := filepath.Join(root, dir)
	if err := os.MkdirAll(filepath.Join(abs, "testpkg", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, "buf.gen.yaml"), []byte(testBufGenYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, "testpkg", "v1", "ping.proto"), []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoModPinningInstalledProtocGenGo(t, root)
	return root, dir
}

// writeGoModPinningInstalledProtocGenGo writes root/go.mod requiring
// google.golang.org/protobuf at the version protoc-gen-go on PATH reports.
func writeGoModPinningInstalledProtocGenGo(t *testing.T, root string) {
	t.Helper()
	path, err := exec.LookPath("protoc-gen-go")
	if err != nil {
		t.Skip("protoc-gen-go not on PATH; skipping tests that need real generation")
	}
	version, err := pluginVersion(path)
	if err != nil {
		t.Fatalf("protoc-gen-go --version: %v", err)
	}
	mod := "module fake\n\ngo 1.26\n\nrequire google.golang.org/protobuf v" + version + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
}

// generateInto runs the real buf toolchain against dir and lands its output
// at out/gen (mirroring what `make proto-v2` leaves committed) — the test's
// way of "adopting" a clean baseline to diff against.
func generateInto(t *testing.T, dir, out string) {
	t.Helper()
	if err := generate(dir, filepath.Dir(out)); err != nil {
		t.Fatalf("buf generate: %v", err)
	}
}

func requireBuf(t *testing.T) {
	t.Helper()
	if _, err := lookBuf(); err != nil {
		t.Skip("buf not on PATH; skipping tests that need real generation")
	}
}

func TestCheck_NoSourceNoCommitted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Check(root, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if result.Checked != 0 || len(result.Diffs) != 0 {
		t.Fatalf("want an honest empty result for a module with no .proto sources and nothing committed, got %+v", result)
	}
}

func TestCheck_OrphanedCommittedFile(t *testing.T) {
	root := t.TempDir()
	dir := "orphan"
	abs := filepath.Join(root, dir)
	if err := os.MkdirAll(filepath.Join(abs, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A committed generated file with no .proto behind it anymore — the
	// half-move shape: the source was deleted but the output wasn't.
	if err := os.WriteFile(filepath.Join(abs, "gen", "stale.pb.go"), []byte("package gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Checked != 1 {
		t.Fatalf("want 1 file checked (the orphan), got %d", result.Checked)
	}
	if len(result.Diffs) != 1 || result.Diffs[0].Kind != "committed" || result.Diffs[0].Rel != "stale.pb.go" {
		t.Fatalf("want one 'committed'-only diff for stale.pb.go, got %+v", result.Diffs)
	}
}

func TestCheck_ModuleDirDoesNotExist(t *testing.T) {
	root := t.TempDir()
	result, err := Check(root, "never-created")
	if err != nil {
		t.Fatal(err)
	}
	if result.Checked != 0 || len(result.Diffs) != 0 {
		t.Fatalf("want an empty result for a module directory that was never created, got %+v", result)
	}
}

func TestCheck_BufNotOnPath(t *testing.T) {
	root, dir := newModule(t)
	t.Setenv("PATH", "")

	_, err := Check(root, dir)
	if err == nil {
		t.Fatal("want an error when buf is not on PATH and the module has .proto sources")
	}
	if !strings.Contains(err.Error(), "buf not found") {
		t.Fatalf("want a clear 'buf not found' error, got: %v", err)
	}
}

func TestCheck_BufGenerateFails(t *testing.T) {
	requireBuf(t)
	root, dir := newModule(t)
	// A plugin that cannot possibly exist turns `buf generate` into a hard
	// error — Check must surface it, not swallow it as "no diffs".
	if err := os.WriteFile(filepath.Join(root, dir, "buf.gen.yaml"), []byte(`version: v2
plugins:
  - local: protoc-gen-does-not-exist-anywhere
    out: gen
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Check(root, dir)
	if err == nil {
		t.Fatal("want an error when buf generate itself fails")
	}
	if !strings.Contains(err.Error(), "buf generate") {
		t.Fatalf("want the error to name the failing step, got: %v", err)
	}
}

func TestCheck_FullCycle(t *testing.T) {
	requireBuf(t)
	root, dir := newModule(t)
	abs := filepath.Join(root, dir)

	// Nothing committed yet: every freshly generated file is "generated"-only.
	before, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Diffs) == 0 {
		t.Fatal("want at least one diff before anything is committed")
	}
	for _, d := range before.Diffs {
		if d.Kind != "generated" {
			t.Fatalf("want every diff to be 'generated' before a gen/ tree exists, got %+v", d)
		}
	}

	// Adopt: generate for real into the committed location, matching what
	// `make proto-v2` would leave behind.
	generateInto(t, abs, filepath.Join(abs, "gen"))

	clean, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Checked == 0 {
		t.Fatal("want a non-zero file count once something is actually committed")
	}
	if len(clean.Diffs) != 0 {
		var b strings.Builder
		PrintReport(&b, clean)
		t.Fatalf("want no diffs right after adopting generated output:\n%s", b.String())
	}

	// Corrupt one committed file: must be reported as "differs".
	var target string
	err = filepath.WalkDir(filepath.Join(abs, "gen"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && target == "" {
			target = path
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if target == "" {
		t.Fatal("expected at least one generated file to corrupt")
	}
	orig, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(bytes.Clone(orig), []byte("\n// hand-edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty.Diffs) != 1 || dirty.Diffs[0].Kind != "differs" {
		t.Fatalf("want exactly one 'differs' diff after corrupting a committed file, got %+v", dirty.Diffs)
	}
	var report strings.Builder
	PrintReport(&report, dirty)
	if !strings.Contains(report.String(), "1 generated files checked, 1 with differences") {
		t.Fatalf("report must state the checked/diff counts, got:\n%s", report.String())
	}

	// Restore, then delete the .proto: the committed output becomes an orphan (Kind
	// "committed").
	if err := os.WriteFile(target, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(abs, "testpkg")); err != nil {
		t.Fatal(err)
	}

	afterRemoval, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRemoval.Diffs) == 0 {
		t.Fatal("want the now-sourceless gen/ tree to be reported as orphaned")
	}
	for _, d := range afterRemoval.Diffs {
		if d.Kind != "committed" {
			t.Fatalf("want every diff to be 'committed' (orphaned) once the .proto is gone, got %+v", d)
		}
	}
}

func TestCheck_ScanErrorPropagates(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	dir := "locked"
	abs := filepath.Join(root, dir)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(abs, 0o755) })

	_, err := Check(root, dir)
	if err == nil {
		t.Fatal("want an error when the module directory itself cannot be read")
	}
	if !strings.Contains(err.Error(), "scan") {
		t.Fatalf("want the scan step named in the error, got: %v", err)
	}
}

func TestAnyProtoFiles_PermissionDenied(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	if _, err := anyProtoFiles(locked); err == nil {
		t.Fatal("want an error when the directory cannot be read")
	}
}

func TestReadTree_UnreadableFile(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(real, "locked.pb.go")
	if err := os.WriteFile(locked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })

	if _, err := readTree(real); err == nil {
		t.Fatal("want an error when a file under the tree cannot be read")
	}
}

func TestAnyProtoFiles(t *testing.T) {
	root := t.TempDir()

	found, err := anyProtoFiles(filepath.Join(root, "missing"))
	if err != nil || found {
		t.Fatalf("nonexistent dir: want (false, nil), got (%v, %v)", found, err)
	}

	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err = anyProtoFiles(empty)
	if err != nil || found {
		t.Fatalf("empty dir: want (false, nil), got (%v, %v)", found, err)
	}

	genOnly := filepath.Join(root, "gen-only")
	if err := os.MkdirAll(filepath.Join(genOnly, "gen", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(genOnly, "gen", "v1", "leftover.proto"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err = anyProtoFiles(genOnly)
	if err != nil || found {
		t.Fatalf("a .proto under gen/ must not count: want (false, nil), got (%v, %v)", found, err)
	}

	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, "a", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "a", "v1", "x.proto"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err = anyProtoFiles(nested)
	if err != nil || !found {
		t.Fatalf("nested .proto outside gen/: want (true, nil), got (%v, %v)", found, err)
	}
}

func TestReadTree(t *testing.T) {
	root := t.TempDir()

	files, err := readTree(filepath.Join(root, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("a missing root is an empty tree, got %v", files)
	}

	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "sub", "b.go"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err = readTree(real)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || string(files["a.go"]) != "a" || string(files["sub/b.go"]) != "b" {
		t.Fatalf("unexpected tree contents: %v", files)
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/taubyte/tau\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if found != root {
		t.Fatalf("want %q, got %q", root, found)
	}

	elsewhere := t.TempDir()
	if _, err := FindRepoRoot(elsewhere); err == nil {
		t.Fatal("want an error when no tau go.mod is above dir")
	}
}

func TestPrintReport(t *testing.T) {
	var b strings.Builder
	PrintReport(&b, Result{Dir: "pkg/proto", Checked: 3, Diffs: []Diff{
		{Rel: "a.pb.go", Kind: "generated"},
		{Rel: "b.pb.go", Kind: "committed"},
		{Rel: "c.pb.go", Kind: "differs"},
	}})
	out := b.String()
	for _, want := range []string{
		"only in regenerated output",
		"only in the committed tree (orphaned",
		"regenerated output differs",
		"pkg/proto: 3 generated files checked, 3 with differences",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q, got:\n%s", want, out)
		}
	}

	b.Reset()
	PrintReport(&b, Result{Dir: "pkg/proto", Checked: 5})
	if !strings.Contains(b.String(), "pkg/proto: 5 generated files checked, 0 with differences") {
		t.Fatalf("clean report missing the summary line, got:\n%s", b.String())
	}
}

const besideBufGenYAML = `version: v2
plugins:
  - local: protoc-gen-go
    out: .
    opt: paths=source_relative
`

func TestCheck_BesideSources(t *testing.T) {
	requireBuf(t)
	root := t.TempDir()
	dir := "modB"
	abs := filepath.Join(root, dir)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"buf.gen.yaml": besideBufGenYAML,
		"ping.proto":   testProto,
		"generate.go":  "package ping\n",
	} {
		if err := os.WriteFile(filepath.Join(abs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeGoModPinningInstalledProtocGenGo(t, root)

	before, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Diffs) != 1 || before.Diffs[0].Kind != "generated" || before.Diffs[0].Rel != "ping.pb.go" {
		t.Fatalf("want ping.pb.go reported as missing, got %+v", before.Diffs)
	}

	if err := generate(abs, abs); err != nil {
		t.Fatal(err)
	}
	clean, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Checked != 1 || len(clean.Diffs) != 0 {
		t.Fatalf("want one clean generated file, got %d checked, %+v", clean.Checked, clean.Diffs)
	}

	target := filepath.Join(abs, "ping.pb.go")
	orig, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(bytes.Clone(orig), "\n// hand-edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := Check(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty.Diffs) != 1 || dirty.Diffs[0].Kind != "differs" {
		t.Fatalf("want one 'differs' diff, got %+v", dirty.Diffs)
	}
}

func TestModuleOut(t *testing.T) {
	dir := t.TempDir()
	if out, err := moduleOut(dir); err != nil || out != "gen" {
		t.Fatalf("no buf.gen.yaml: got %q, %v", out, err)
	}
	for _, c := range []struct {
		yaml, want string
		fails      bool
	}{
		{yaml: testBufGenYAML, want: "gen"},
		{yaml: besideBufGenYAML, want: "."},
		{yaml: "version: v2\nplugins: []\n", want: "gen"},
		{yaml: "version: v2\nplugins:\n  - local: a\n    out: gen\n  - local: b\n    out: .\n", fails: true},
		{yaml: "plugins: [", fails: true},
	} {
		if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte(c.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := moduleOut(dir)
		if c.fails {
			if err == nil {
				t.Fatalf("%q: want an error, got %q", c.yaml, out)
			}
			continue
		}
		if err != nil || out != c.want {
			t.Fatalf("%q: got %q, %v, want %q", c.yaml, out, err, c.want)
		}
	}
}
