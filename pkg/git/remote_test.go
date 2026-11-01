package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gotest.tools/v3/assert"
)

// localRemote is a bare repository on disk with one commit on main and a "dream"
// branch beside it, as a file:// URL.
func localRemote(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	main := gogit.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}
	_, err := gogit.PlainInitWithOptions(bare, &gogit.PlainInitOptions{Bare: true, InitOptions: main})
	assert.NilError(t, err)

	work := t.TempDir()
	repo, err := gogit.PlainInitWithOptions(work, &gogit.PlainInitOptions{InitOptions: main})
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(work, "README"), []byte("fixture\n"), 0o644))
	wt, err := repo.Worktree()
	assert.NilError(t, err)
	_, err = wt.Add("README")
	assert.NilError(t, err)
	head, err := wt.Commit("fixture", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Unix(1700000000, 0)},
	})
	assert.NilError(t, err)
	assert.NilError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("dream"), head)))

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"file://" + bare}})
	assert.NilError(t, err)
	assert.NilError(t, repo.Push(&gogit.PushOptions{RefSpecs: []config.RefSpec{"refs/heads/*:refs/heads/*"}}))
	return "file://" + bare
}

func TestListRemoteReadsTheHeadsWithoutCloning(t *testing.T) {
	url := localRemote(t)
	refs, err := ListRemote(t.Context(), URL(url))
	assert.NilError(t, err)
	names := map[plumbing.ReferenceName]bool{}
	for _, r := range refs {
		names[r.Name()] = true
	}
	assert.Assert(t, names["refs/heads/main"] && names["refs/heads/dream"])

	_, err = ListRemote(t.Context())
	assert.ErrorContains(t, err, "needs its url")
	_, err = ListRemote(t.Context(), URL(url), SSHKeyAs("git", "k", nil))
	assert.ErrorContains(t, err, "host keys")
}
