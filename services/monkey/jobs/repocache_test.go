package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func TestRepoDirSerializesSameKey(t *testing.T) {
	gitCacheRoot = t.TempDir()
	var inside, max int32
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			_, unlock, err := lockRepoDir(context.Background(), "projA", "1")
			assert.NilError(t, err)
			n := atomic.AddInt32(&inside, 1)
			if n > atomic.LoadInt32(&max) {
				atomic.StoreInt32(&max, n)
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			unlock()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	assert.Equal(t, atomic.LoadInt32(&max), int32(1))

	// another project does not wait
	_, u1, _ := lockRepoDir(context.Background(), "projA", "1")
	d2, u2, err := lockRepoDir(context.Background(), "projB", "1")
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(filepath.Dir(d2)), "projB")
	u1()
	u2()
}

func TestRepoCachePrunesOldIdleDirs(t *testing.T) {
	gitCacheRoot = t.TempDir()
	old, held := filepath.Join(gitCacheRoot, "p", "old"), filepath.Join(gitCacheRoot, "p", "held")
	for _, d := range []string{old, held} {
		assert.NilError(t, os.MkdirAll(d, 0755))
		assert.NilError(t, os.WriteFile(d+".lock", nil, 0644))
		past := time.Now().Add(-8 * 24 * time.Hour)
		assert.NilError(t, os.Chtimes(d, past, past))
	}
	l := repoLock(held)
	l <- struct{}{}
	lastPrune = time.Time{}
	pruneRepoCache()
	<-l
	_, err := os.Stat(old)
	assert.Assert(t, os.IsNotExist(err))
	_, err = os.Stat(held)
	assert.NilError(t, err)
	_, err = os.Stat(old + ".lock")
	assert.Assert(t, os.IsNotExist(err))
}

// A clone dir held by a monkey in another process (its flock) is waited on.
func TestRepoDirWaitsForOtherProcess(t *testing.T) {
	gitCacheRoot = t.TempDir()
	dir := filepath.Join(gitCacheRoot, "projA", "1")
	assert.NilError(t, os.MkdirAll(filepath.Dir(dir), 0755))
	other, err := fileLock(context.Background(), dir+".lock")
	assert.NilError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err = lockRepoDir(ctx, "projA", "1")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	other()
	_, unlock, err := lockRepoDir(context.Background(), "projA", "1")
	assert.NilError(t, err)
	unlock()
}

// A dir the handler locks (the config clone of a code job) is released when
// the job ends: the handler works on a copy of the job's Context.
func TestHandlerReposReleasedByRun(t *testing.T) {
	gitCacheRoot = t.TempDir()
	c := &Context{ctx: context.Background(), ProjectID: "projA", RepoType: repositorytype.CodeRepository}
	c.Job = &patrick.Job{}
	c.LogFile, _ = os.CreateTemp(t.TempDir(), "log")
	h, err := c.Handler()
	assert.NilError(t, err)
	h.(*code).openRepo("1", "file:///nonexistent", "", "")
	c.releaseRepos()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, unlock, err := lockRepoDir(ctx, "projA", "1")
	assert.NilError(t, err)
	unlock()
}

// A clone whose .git outgrew the limit is dropped and cloned afresh.
func TestOpenRepoDropsOversizedClone(t *testing.T) {
	gitCacheRoot, repoGitMaxBytes = t.TempDir(), 1024
	defer func() { repoGitMaxBytes = 256 << 20 }()
	dir := filepath.Join(gitCacheRoot, "projA", "1")
	assert.NilError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0755))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, ".git", "pack"), make([]byte, 2048), 0644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "stale"), nil, 0644))

	c := &Context{ctx: context.Background(), ProjectID: "projA"}
	c.Job = &patrick.Job{}
	c.LogFile, _ = os.CreateTemp(t.TempDir(), "log")
	c.openRepo("1", "file:///nonexistent", "", "") // the clone fails; the dir must already be gone
	c.releaseRepos()
	_, err := os.Stat(filepath.Join(dir, "stale"))
	assert.Assert(t, os.IsNotExist(err))
}
