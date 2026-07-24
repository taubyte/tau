package codefile

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// Write scaffolds a resource's code dir from a template: template files are
// copied (config.yaml skipped), and a sibling common/ dir is merged in.
func TestWriteFromTemplate(t *testing.T) {
	root := t.TempDir()
	tmpl := filepath.Join(root, "templates", "go")
	assert.NilError(t, os.MkdirAll(tmpl, 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(tmpl, "main.go"), []byte("package main"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(tmpl, "config.yaml"), []byte("skip: me"), 0o644))
	// a sibling common/ dir is merged
	common := filepath.Join(root, "templates", "common")
	assert.NilError(t, os.MkdirAll(common, 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(common, "go.mod"), []byte("module x"), 0o644))

	dst := CodePath(filepath.Join(root, "out"))
	assert.NilError(t, dst.Write(tmpl, "fn"))

	assert.Equal(t, read(t, filepath.Join(dst.String(), "main.go")), "package main")
	assert.Equal(t, read(t, filepath.Join(dst.String(), "go.mod")), "module x")
	_, err := os.Stat(filepath.Join(dst.String(), "config.yaml"))
	assert.Assert(t, os.IsNotExist(err), "config.yaml must not be copied")
}

// With no template, Write drops a placeholder markdown named after the resource.
func TestWriteNoTemplate(t *testing.T) {
	dst := CodePath(filepath.Join(t.TempDir(), "out"))
	assert.NilError(t, dst.Write("", "mylib"))
	_, err := os.Stat(filepath.Join(dst.String(), "mylib.md"))
	assert.NilError(t, err)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	assert.NilError(t, err)
	return string(b)
}
