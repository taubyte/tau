package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func commitAndPush(t *testing.T, r *gogit.Repository, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
		t.Fatal(err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Add(name); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@t", When: time.Now()}
	if _, err = w.Commit(name, &gogit.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err = r.Push(&gogit.PushOptions{RefSpecs: []config.RefSpec{"refs/heads/*:refs/heads/*"}}); err != nil {
		t.Fatal(err)
	}
}

// A pull with nothing new is not an error.
func TestPullAlreadyUpToDate(t *testing.T) {
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}

	seedDir := filepath.Join(tmp, "seed")
	seed, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	commitAndPush(t, seed, seedDir, "a")

	repo, err := New(context.Background(), URL(bare), Root(filepath.Join(tmp, "clone")))
	if err != nil {
		t.Fatal(err)
	}

	commitAndPush(t, seed, seedDir, "b")
	if err = repo.Pull(); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if err = repo.Pull(); err != nil {
		t.Fatalf("second pull (already up to date) must return nil, got: %v", err)
	}
}
