package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func TestPushNewSaysWhenNothingWasSent(t *testing.T) {
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

	clone := filepath.Join(tmp, "clone")
	repo, err := New(context.Background(), URL(bare), Root(clone), Author("t", "t@t"))
	if err != nil {
		t.Fatal(err)
	}

	if sent, err := repo.PushNew(); err != nil || sent {
		t.Fatalf("remote already has this commit: sent %v, err %v", sent, err)
	}

	if err = os.WriteFile(filepath.Join(clone, "b"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = repo.Commit("b", "."); err != nil {
		t.Fatal(err)
	}
	if sent, err := repo.PushNew(); err != nil || !sent {
		t.Fatalf("a new commit: sent %v, err %v", sent, err)
	}
}
