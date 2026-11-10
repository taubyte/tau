package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// tau saves a session onto the config repo through an OS-backed BasePathFs. On
// Windows afero.Walk yields backslash paths ("\.git"), which once joined to "/"
// became the absolute "/\.git" the BasePathFs refuses ("mkdir /\.git").
func TestCopyTreeOntoOSBasePath(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/.git/HEAD", []byte("ref"), 0o644))
	assert.NilError(t, afero.WriteFile(src, "/config.yaml", []byte("id: x"), 0o644))

	root := t.TempDir()
	assert.NilError(t, copyTree(src, "/", afero.NewBasePathFs(afero.NewOsFs(), root), "/"))

	_, err := afero.ReadFile(afero.NewOsFs(), filepath.Join(root, "config.yaml"))
	assert.NilError(t, err)
	_, err = os.Stat(filepath.Join(root, ".git"))
	assert.Assert(t, os.IsNotExist(err), ".git must not be copied")
}

// noWriteFs fails every open-for-write, as Windows does on read-only git objects.
type noWriteFs struct{ afero.Fs }

func (f noWriteFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}
	return f.Fs.OpenFile(name, flag, perm)
}

func (f noWriteFs) Create(name string) (afero.File, error) {
	return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
}

// Saving a session in place (tau's flush) writes neither .git nor unchanged files.
func TestCopyTreeInPlaceWritesNothingUnchanged(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/.git/objects/3a/915e", []byte("obj"), 0o444))
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: x"), 0o644))
	ro := noWriteFs{fs}
	assert.NilError(t, copyTree(ro, "/", ro, "/"))

	// A changed file is still written.
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/config.yaml", []byte("id: y"), 0o644))
	assert.NilError(t, copyTree(src, "/", fs, "/"))
	b, _ := afero.ReadFile(fs, "/config.yaml")
	assert.Equal(t, string(b), "id: y")
}
