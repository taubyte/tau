package compile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pterm/pterm"
)

// cacheSalt busts every committed asset at once. Bump it when the build
// toolchain or .zwasm format changes in a way source hashing can't see.
const cacheSalt = "v1"

// stashCached serves a committed, source-hashed build asset when one exists,
// otherwise runs build() and writes the result next to the source as
// <base>.<hash>.zwasm so future runs skip the (slow) container build. The
// asset is rebuilt only when the source changes (its hash flips). Callers that
// exist to test the build toolchain set ForceBuild to bypass the cache.
//
// ponytail: writing into the source tree on a miss IS the regenerator — no
// separate `make build-fixtures` target. A stale/absent asset just means one
// slow build that self-heals once the freshly-written asset is committed.
func (ctx resourceContext) stashCached(id string, build func() (io.ReadSeekCloser, error)) error {
	cachePath, base, herr := ctx.cachePath()

	if herr == nil && !ctx.forceBuild {
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

	if herr != nil || ctx.forceBuild {
		return ctx.stashAndPush(id, reader)
	}

	// Buffer so we can both persist the asset and replay it to the stash.
	buf, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return err
	}
	writeCache(cachePath, base, buf) // best-effort; a read-only tree just rebuilds next time

	return ctx.stashAndPush(id, readSeekNopCloser{bytes.NewReader(buf)})
}

// cachePath derives the committed asset path from a content hash of every
// source path (files and directory trees), so any edit produces a new name.
func (ctx resourceContext) cachePath() (path, base string, err error) {
	h := sha256.New()
	io.WriteString(h, cacheSalt)

	paths := append([]string(nil), ctx.paths...)
	sort.Strings(paths)
	for _, p := range paths {
		if err = hashSource(h, p); err != nil {
			return "", "", err
		}
	}

	sum := hex.EncodeToString(h.Sum(nil))[:16]
	first := ctx.paths[0]
	base = strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
	return filepath.Join(filepath.Dir(first), base+"."+sum+".zwasm"), base, nil
}

// hashSource folds a file's bytes (or every file under a directory, in lexical
// order with its relative name) into h.
func hashSource(h io.Writer, p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return hashFile(h, p, filepath.Base(p))
	}
	return filepath.WalkDir(p, func(wp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(p, wp)
		return hashFile(h, wp, rel)
	})
}

func hashFile(h io.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	io.WriteString(h, name)
	_, err = io.Copy(h, f)
	return err
}

// writeCache atomically writes the asset and prunes stale variants of the same
// source, so exactly one <base>.*.zwasm survives and git shows a clean swap.
func writeCache(cachePath, base string, buf []byte) {
	dir := filepath.Dir(cachePath)
	if olds, _ := filepath.Glob(filepath.Join(dir, base+".*.zwasm")); olds != nil {
		for _, o := range olds {
			if o != cachePath {
				os.Remove(o)
			}
		}
	}

	tmp, err := os.CreateTemp(dir, ".zwasm-*")
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
