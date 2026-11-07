package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
}
