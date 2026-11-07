package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/taubyte/tau/pkg/git"
	"gotest.tools/v3/assert"
)

// A repo with nothing new to commit is pushed as it is, not reported failed.
func TestPushRepoCleanTree(t *testing.T) {
	bare, work := t.TempDir(), filepath.Join(t.TempDir(), "tb_code_x")
	_, err := gogit.PlainInit(bare, true)
	assert.NilError(t, err)
	r, err := gogit.PlainInit(work, false)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(work, "a"), []byte("a"), 0o644))
	w, _ := r.Worktree()
	w.Add("a")
	head, err := w.Commit("init", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
	assert.NilError(t, err)
	_, err = r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}})
	assert.NilError(t, err)

	repo, err := git.New(context.Background(), git.URL(git.LocalURIScheme+work), git.Author("t", "t@t"))
	assert.NilError(t, err)
	pushed, err := pushRepo(repo, "msg")
	assert.NilError(t, err)
	assert.Equal(t, pushed.Commit, head.String())
	assert.Equal(t, pushed.Remote, filepath.Base(bare))
}
