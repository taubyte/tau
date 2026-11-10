//go:build unix

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// A waiter whose lock file was unlinked by a prune must not take the flock on
// the dead inode: the next job, opening a new file, would hold "the" lock too.
func TestLockFileUnlinkedWhileWaitingIsNotHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.lock")
	first, err := fileLock(context.Background(), path)
	assert.NilError(t, err)

	got := make(chan func(), 1)
	go func() {
		unlock, err := fileLock(context.Background(), path)
		assert.Check(t, err == nil, "%v", err)
		got <- unlock
	}()
	time.Sleep(300 * time.Millisecond) // the waiter has the old file open

	assert.NilError(t, os.Remove(path))
	first()
	unlock := <-got
	defer unlock()

	_, err = fileLock(nil, path)
	assert.ErrorIs(t, err, errRepoBusy)
}
