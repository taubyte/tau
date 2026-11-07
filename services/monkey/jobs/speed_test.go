package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/tns"
	"gotest.tools/v3/assert"
)

func TestHashDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0755))
		assert.NilError(t, os.WriteFile(p, []byte(content), 0644))
	}
	write("main.go", "package lib")
	write(".taubyte/config.yaml", "image: taubyte/go-wasi:latest")

	first, err := hashDir(dir)
	assert.NilError(t, err)

	again, err := hashDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, first, again)

	write(".git/HEAD", "ref: refs/heads/main")
	withGit, err := hashDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, first, withGit, ".git must not change the hash")

	write(".taubyte/config.yaml", "image: taubyte/go-wasi:v2")
	newImage, err := hashDir(dir)
	assert.NilError(t, err)
	assert.Assert(t, newImage != first, "a different build image must change the hash")

	write("main.go", "package lib\n")
	edited, err := hashDir(dir)
	assert.NilError(t, err)
	assert.Assert(t, edited != newImage, "an edit must change the hash")

	assert.NilError(t, os.Rename(filepath.Join(dir, "main.go"), filepath.Join(dir, "other.go")))
	renamed, err := hashDir(dir)
	assert.NilError(t, err)
	assert.Assert(t, renamed != edited, "a rename must change the hash")
}

func TestDevBuildSlotCount(t *testing.T) {
	const gib = 1 << 30
	assert.Equal(t, buildSlotCount(12, 64*gib), 6, "half the cores")
	assert.Equal(t, buildSlotCount(12, 7*gib+gib/2), 6)
	assert.Equal(t, buildSlotCount(12, 3*gib), 3, "a GiB per build")
	assert.Equal(t, buildSlotCount(64, 256*gib), 8, "at most 8")
	assert.Equal(t, buildSlotCount(1, 512<<20), 1, "at least 1")
	assert.Equal(t, buildSlotCount(8, 0), 2, "memory unknown")
}

func TestRetryTns(t *testing.T) {
	defer func(d time.Duration) { TnsRetryFor = d }(TnsRetryFor)
	TnsRetryFor = 5 * time.Second

	calls := 0
	start := time.Now()
	_, err := retryTns(context.Background(), func() (tns.Object, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("not yet")
		}
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, calls, 3)
	assert.Assert(t, time.Since(start) < 4*time.Second)

	TnsRetryFor = 1500 * time.Millisecond
	_, err = retryTns(context.Background(), func() (tns.Object, error) { return nil, errors.New("never") })
	assert.ErrorContains(t, err, "never")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	TnsRetryFor = time.Minute
	start = time.Now()
	_, err = retryTns(ctx, func() (tns.Object, error) { return nil, errors.New("cancelled") })
	assert.ErrorContains(t, err, "cancelled")
	assert.Assert(t, time.Since(start) < time.Second)
}
