package build

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/core/builders"
	"gotest.tools/v3/assert"
)

func TestSandboxSource_CopiesTreeAndRespectsGitignore(t *testing.T) {
	src := t.TempDir()

	mustWrite(t, filepath.Join(src, "main.go"), "package main\n")
	mustWrite(t, filepath.Join(src, ".gitignore"), "node_modules/\n*.log\n!keep.log\n")
	mustWrite(t, filepath.Join(src, "node_modules", "pkg", "index.js"), "// big\n")
	mustWrite(t, filepath.Join(src, "debug.log"), "noise\n")
	mustWrite(t, filepath.Join(src, "keep.log"), "wanted\n")
	mustWrite(t, filepath.Join(src, "sub", "ok.txt"), "ok\n")
	mustWrite(t, filepath.Join(src, "sub", ".gitignore"), "secret.txt\n")
	mustWrite(t, filepath.Join(src, "sub", "secret.txt"), "shh\n")

	sandbox, cleanup, err := sandboxSource(src)
	assert.NilError(t, err)
	defer cleanup()

	assertExists(t, filepath.Join(sandbox, "main.go"))
	assertExists(t, filepath.Join(sandbox, ".gitignore"))
	assertExists(t, filepath.Join(sandbox, "keep.log"))
	assertExists(t, filepath.Join(sandbox, "sub", "ok.txt"))

	assertMissing(t, filepath.Join(sandbox, "node_modules"))
	assertMissing(t, filepath.Join(sandbox, "debug.log"))
	assertMissing(t, filepath.Join(sandbox, "sub", "secret.txt"))
}

func TestSandboxSource_CleanupRemovesSandbox(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "a\n")

	sandbox, cleanup, err := sandboxSource(src)
	assert.NilError(t, err)
	assertExists(t, sandbox)

	cleanup()
	_, err = os.Stat(sandbox)
	assert.Assert(t, os.IsNotExist(err), "sandbox should be removed, got err=%v", err)
}

func TestSandboxSource_PreservesSymlinks(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "target.txt"), "hi\n")
	assert.NilError(t, os.Symlink("target.txt", filepath.Join(src, "link.txt")))

	sandbox, cleanup, err := sandboxSource(src)
	assert.NilError(t, err)
	defer cleanup()

	link := filepath.Join(sandbox, "link.txt")
	info, err := os.Lstat(link)
	assert.NilError(t, err)
	assert.Assert(t, info.Mode()&os.ModeSymlink != 0, "expected symlink, got %v", info.Mode())
	dest, err := os.Readlink(link)
	assert.NilError(t, err)
	assert.Equal(t, dest, "target.txt")
}

// TestSandboxedBuild_BuildsFromSandboxNotSource is the regression guard for #432:
// the builder must be constructed against a disposable sandbox, not the user's
// workDir, and the sandbox must be removed on Close. Guards against the sandbox
// being unwired from newBuilderFunc (which is how the original fix got lost).
func TestSandboxedBuild_BuildsFromSandboxNotSource(t *testing.T) {
	workDir := t.TempDir()
	mustWrite(t, filepath.Join(workDir, "main.go"), "package main\n")

	var builtFrom string
	inner := func(_ context.Context, _ io.Writer, dir string) (builders.Builder, error) {
		builtFrom = dir
		return &mockBuilder{}, nil
	}

	b, err := sandboxedBuild(context.Background(), io.Discard, workDir, inner)
	assert.NilError(t, err)

	// built from a sandbox copy, not the user's source dir
	assert.Assert(t, builtFrom != workDir, "build ran against the user's source dir")
	assert.Assert(t, builtFrom != "" && builtFrom != workDir)
	assertExists(t, filepath.Join(builtFrom, "main.go"))

	// simulate the container polluting the sandbox; the user's dir stays clean
	mustWrite(t, filepath.Join(builtFrom, "node_modules", "junk.js"), "x\n")
	assertMissing(t, filepath.Join(workDir, "node_modules"))

	// Close disposes the sandbox
	assert.NilError(t, b.Close())
	_, err = os.Stat(builtFrom)
	assert.Assert(t, os.IsNotExist(err), "sandbox should be removed after Close, got err=%v", err)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	assert.NilError(t, err, "expected %s to exist", path)
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	assert.Assert(t, os.IsNotExist(err), "expected %s to be absent, got err=%v", path, err)
}
