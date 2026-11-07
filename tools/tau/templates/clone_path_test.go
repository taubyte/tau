package templates

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gotest.tools/v3/assert"
)

// The template cache lives under os.TempDir(), a backslash path on Windows:
// cloning there and copying out must work with the OS separators.
func TestTemplateCloneUnderTempDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "tb_empty")
	r, err := gogit.PlainInit(src, false)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(filepath.Join(src, "code"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(src, "code", "main.go"), []byte("package lib"), 0o644))
	w, _ := r.Worktree()
	w.Add("code/main.go")
	_, err = w.Commit("init", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
	assert.NilError(t, err)

	prev := templateFolder
	templateFolder = filepath.Join(t.TempDir(), "taubyte_templates")
	t.Cleanup(func() { templateFolder = prev })
	assert.NilError(t, os.MkdirAll(templateFolder, 0o755))

	dst := t.TempDir()
	assert.NilError(t, (&TemplateInfo{URL: "file://" + filepath.ToSlash(src)}).CloneTo(dst))
	_, err = os.Stat(filepath.Join(dst, "code", "main.go"))
	assert.NilError(t, err)
}
