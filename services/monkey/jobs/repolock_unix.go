//go:build unix

package jobs

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

var errRepoBusy = errors.New("repo dir held by another process")

// fileLock holds path with flock so monkeys in other processes on this host
// never share a clone dir. A nil ctx tries once.
func fileLock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { f.Close() }, nil // closing releases the flock
		}
		if ctx == nil {
			f.Close()
			return nil, errRepoBusy
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
