package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/samyfodil/wazy"
)

// cacheDir is where compiled modules live under the node's data root. wazy
// namespaces its own contents below it by version, arch and OS, so a repin
// starts a fresh set instead of reading another build's.
const cacheDir = "vm/cache"

var (
	cacheLock sync.Mutex
	cachePath string
	cache     wazy.CompilationCache
)

// SetCacheRoot puts the compilation cache under the node's data root: the
// directory `--root` names and config.Config.Root() reports. Compiling a module
// costs orders of magnitude more than instantiating one, so a cache that dies
// with the process is paid for again on the first call after every restart.
//
// It has to be called before the first runtime is built. Once the cache is open
// the runtimes holding it cannot be moved to another directory, so a late call
// is refused rather than quietly ignored.
func SetCacheRoot(root string) error {
	if root == "" {
		return errors.New("vm cache root cannot be empty")
	}

	dir := filepath.Join(root, cacheDir)
	c, err := wazy.NewCompilationCacheWithDir(dir)
	if err != nil {
		return fmt.Errorf("opening vm compilation cache at `%s` failed with: %w", dir, err)
	}

	cacheLock.Lock()
	defer cacheLock.Unlock()

	if cache != nil {
		c.Close(context.Background())
		return fmt.Errorf("vm compilation cache already open at `%s`", cachePath)
	}

	cache, cachePath = c, dir

	return nil
}

// Cache is the compilation cache every runtime shares, opened on first use.
// With no data root set it falls back to a temp directory, which is what this
// always did: a test or a one-shot tool has nowhere durable to write and still
// has to run.
func Cache() wazy.CompilationCache {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	if cache == nil {
		dir, err := os.MkdirTemp("", "wazy")
		if err != nil {
			panic(err)
		}

		if cache, err = wazy.NewCompilationCacheWithDir(dir); err != nil {
			panic(err)
		}

		cachePath = dir
	}

	return cache
}
