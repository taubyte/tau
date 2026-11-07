package session

import (
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

	for _, f := range []string{filepath.Join(".git", "HEAD"), "config.yaml"} {
		_, err := afero.ReadFile(afero.NewOsFs(), filepath.Join(root, f))
		assert.NilError(t, err, f)
	}
}
