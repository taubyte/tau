package compile

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pterm/pterm"
)

// stashCached serves a committed <base>.zwasm sitting next to the source when it
// is at least as new as the source, otherwise runs build() and writes the result
// back so the next run skips the (slow) container build. The asset is rebuilt
// only when the source is touched (mtime), and the stable name means git sees a
// clean modify rather than a churn of hash-named files. Callers that exist to
// test the build toolchain set ForceBuild to bypass the cache.
//
// ponytail: mtime, not a content hash — keeps the committed asset name stable and
// git clean. Caveat: git doesn't preserve mtimes, so commit source and asset
// together; a fresh clone can't detect an asset that was committed stale.
func (ctx resourceContext) stashCached(id string, build func() (io.ReadSeekCloser, error)) error {
	cachePath := ctx.cachePath()

	if !ctx.forceBuild && cacheFresh(cachePath, ctx.paths) {
		if f, err := os.Open(cachePath); err == nil {
			defer f.Close()
			pterm.Info.Printf("using cached asset %s\n", cachePath)
			return ctx.stashAndPush(id, f)
		}
	}

	reader, err := build()
	if err != nil {
		return err
	}

	if ctx.forceBuild {
		return ctx.stashAndPush(id, reader)
	}

	// Buffer so we can both persist the asset and replay it to the stash.
	buf, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return err
	}
	writeCache(cachePath, buf) // best-effort; a read-only tree just rebuilds next time

	return ctx.stashAndPush(id, readSeekNopCloser{bytes.NewReader(buf)})
}

// cachePath is the stable asset name for a source: <base>.zwasm next to it.
func (ctx resourceContext) cachePath() string {
	first := ctx.paths[0]
	base := filepath.Base(first)
	base = base[:len(base)-len(filepath.Ext(base))]
	return filepath.Join(filepath.Dir(first), base+".zwasm")
}

// cacheFresh reports whether cachePath exists and is no older than every source
// (the newest mtime across a directory tree).
func cacheFresh(cachePath string, sources []string) bool {
	info, err := os.Stat(cachePath)
	if err != nil {
		return false
	}
	assetTime := info.ModTime()
	for _, p := range sources {
		newest, err := newestMod(p)
		if err != nil || newest.After(assetTime) {
			return false
		}
	}
	return true
}

func newestMod(p string) (time.Time, error) {
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	if !info.IsDir() {
		return info.ModTime(), nil
	}
	newest := info.ModTime()
	err = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	return newest, err
}

// writeCache atomically overwrites the asset in place (stable name → clean git).
func writeCache(cachePath string, buf []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".zwasm-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	os.Rename(tmp.Name(), cachePath)
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }
