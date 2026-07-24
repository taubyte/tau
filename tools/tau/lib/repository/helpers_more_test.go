package repositoryLib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"gotest.tools/v3/assert"
)

func TestGetRepositoryUrl(t *testing.T) {
	assert.Equal(t, GetRepositoryUrl("github", "u/r"), "https://github.com/u/r")
	defer func() { assert.Assert(t, recover() != nil) }()
	GetRepositoryUrl("gitlab", "u/r") // unsupported provider panics
}

func TestPathAndHasBeenCloned(t *testing.T) {
	root := t.TempDir()
	proj := config.Project{Location: root}

	// a website's clone dir is <websiteLoc>/<repo-basename>
	info := &Info{Type: WebsiteRepositoryType, FullName: "taubyte-test/site"}
	p, err := info.path(proj)
	assert.NilError(t, err)
	assert.Assert(t, filepath.Base(p) == "site")

	// absent -> not cloned; create the dir -> cloned
	assert.Assert(t, !info.HasBeenCloned(proj, "github"))
	assert.NilError(t, os.MkdirAll(p, 0o755))
	assert.Assert(t, info.HasBeenCloned(proj, "github"))
	assert.Assert(t, info.isCloned(p))

	// an unknown repo type has no clone location
	assert.Assert(t, !(&Info{Type: "mystery", FullName: "a/b"}).HasBeenCloned(proj, "github"))
}
