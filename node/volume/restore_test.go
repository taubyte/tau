package volume_test

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/volume"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// restorer is a fresh node over root whose store holds archive as the volume's
// last checkpoint, with what it says.
func restorer(t *testing.T, root string, archive []byte) (*volume.Volumes, *said) {
	t.Helper()

	store, index, log := newBlobs(), newRows(), &said{}
	id := store.put(t, archive)
	require.NoError(t, index.Put(t.Context(), recordedAt+"shop/prod/QmData", []byte(id)))

	v, err := volume.New(root, store, &opening{rows: index}, log)
	require.NoError(t, err)
	t.Cleanup(func() { v.Close() })
	return v, log
}

func TestARestoreOverItsQuotaIsServedAndSaid(t *testing.T) {
	for _, c := range []struct {
		name   string
		files  map[string]string
		warned []string
	}{
		{"one file over it", map[string]string{"rows": "123456789"},
			[]string{"volume: shop/prod/QmData restored 9 bytes and declared a quota of 8"}},
		{"files that add up to more", map[string]string{"a": "12345", "b": "12345"},
			[]string{"volume: shop/prod/QmData restored 10 bytes and declared a quota of 8"}},
		{"exactly the quota", map[string]string{"a": "1234", "b": "5678"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, log := restorer(t, t.TempDir(), tarOf(t, c.files))
			dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", map[string]string{"quota": "8"}))
			require.NoError(t, err)
			for name, body := range c.files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				require.Equal(t, body, string(got))
			}
			require.Equal(t, c.warned, log.warned())
		})
	}
}

// disk is a filesystem holding whatever is under root, out of the bytes and
// inodes it was given.
type disk struct {
	root          string
	bytes, inodes int64
}

func (d disk) measure(string) (reserve.Usage, error) {
	var used, entries int64
	err := filepath.WalkDir(d.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == d.root {
			return err
		}
		entries++
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			used += info.Size()
		}
		return err
	})
	return reserve.Usage{
		Size: 100 << 20, Free: uint64(max(d.bytes-used, 0)),
		Files: 100, FreeFiles: uint64(max(d.inodes-entries, 0)),
	}, err
}

// noise is n bytes that are not zeros, so they land on disk, and that do not
// compress, so a restore reads about as many of them from the store as it
// unpacks.
func noise(n int) string {
	b := make([]byte, n)
	rand.NewChaCha8([32]byte{}).Read(b)
	return string(b)
}

func TestARestoreLeavesTheDiskItsReserve(t *testing.T) {
	for _, c := range []struct {
		name    string
		files   map[string]string
		inodes  int64
		refused reserve.Error
	}{
		// Half of a 100MiB disk is held back, and 51MiB is free: the first
		// MiB fits and the second does not.
		{"bytes", map[string]string{"rows": noise(2 << 20)}, 100,
			reserve.Error{Unit: "bytes", Free: 50 << 20, Want: 1 << 20, Reserve: 50 << 20}},
		// Five of a hundred inodes are held back. The volumes directory and
		// the restore's own take two of the nine free, then two files fit.
		{"inodes", map[string]string{"a": "a", "b": "b", "c": "c"}, 9,
			reserve.Error{Unit: "inodes", Free: 5, Want: 1, Reserve: 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			v, _ := restorer(t, root, tarOf(t, c.files))
			volume.HoldTo(v, reserve.Floor{Space: 0.5, Inodes: 0.05, Measure: disk{root: root, bytes: 51 << 20, inodes: c.inodes}.measure})

			_, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
			require.ErrorContains(t, err, "volume: restoring shop/prod/QmData: reserve: the filesystem holding")
			var refused *reserve.Error
			require.ErrorAs(t, err, &refused)
			refused.Path = ""
			require.Equal(t, c.refused, *refused)

			left, err := os.ReadDir(filepath.Join(root, "volumes"))
			require.NoError(t, err)
			require.Empty(t, left, "the refused restore left something behind")
		})
	}
}

// arriving is a checkpoint store whose one archive runs then once more than
// after bytes of it have been read.
type arriving struct {
	*blobs
	after int
	then  func()
}

func (a *arriving) Get(ctx context.Context, id string) (io.ReadSeekCloser, error) {
	content, err := a.blobs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &partway{ReadSeekCloser: content, after: a.after, then: a.then}, nil
}

type partway struct {
	io.ReadSeekCloser
	after, read int
	then        func()
}

func (p *partway) Read(b []byte) (int, error) {
	n, err := p.ReadSeekCloser.Read(b)
	p.read += n
	if p.then != nil && p.read >= p.after {
		p.then()
		p.then = nil
	}
	return n, err
}

func TestWhatElseFillsTheDiskWhileARestoreRunsCounts(t *testing.T) {
	for _, c := range []struct {
		name     string
		meantime bool
	}{{"alone", false}, {"with another writer", true}} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			store, index := newBlobs(), newRows()
			id := store.put(t, tarOf(t, map[string]string{"rows": noise(8 << 20)}))
			require.NoError(t, index.Put(t.Context(), recordedAt+"shop/prod/QmData", []byte(id)))

			other := func() {}
			if c.meantime {
				other = func() { require.NoError(t, os.WriteFile(filepath.Join(root, "other"), []byte(noise(5<<20)), 0o600)) }
			}
			v, err := volume.New(root, &arriving{blobs: store, after: 2 << 20, then: other}, &opening{rows: index}, &said{})
			require.NoError(t, err)
			t.Cleanup(func() { v.Close() })
			volume.HoldTo(v, reserve.Floor{Space: 0.5, Measure: disk{root: root, bytes: 60 << 20, inodes: 100}.measure})

			_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
			if !c.meantime {
				require.NoError(t, err)
				return
			}
			var refused *reserve.Error
			require.ErrorAs(t, err, &refused)
		})
	}
}

// synced is one thing a restore made durable: its path, whether the restore
// was still beside the volume's directory then, and its mode.
type synced struct {
	path   string
	staged bool
	mode   fs.FileMode
}

func TestARestoreIsOnDiskBeforeItIsInPlace(t *testing.T) {
	root := t.TempDir()
	v, _ := restorer(t, root, entries(t,
		tar.Header{Name: "d", Typeflag: tar.TypeDir, Mode: 0o750},
		tar.Header{Name: "d/rows", Typeflag: tar.TypeReg, Mode: 0o640},
		tar.Header{Name: "closed", Typeflag: tar.TypeDir, Mode: 0o500},
		tar.Header{Name: "closed/rows", Typeflag: tar.TypeReg, Mode: 0o640},
		tar.Header{Name: "top", Typeflag: tar.TypeReg, Mode: 0o640},
		linkEntry("link", "top"),
	))
	volumes := filepath.Join(root, "volumes")
	staging := filepath.Join(volumes, "shop%2Fprod%2FQmData%restoring")

	var made []synced
	volume.SyncWith(v, func(file *os.File) error {
		info, err := file.Stat()
		require.NoError(t, err)
		_, err = os.Lstat(staging)
		made = append(made, synced{path: filepath.Clean(file.Name()), staged: err == nil, mode: info.Mode().Perm()})
		return file.Sync()
	})

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "closed"), 0o700) })

	require.NotEmpty(t, made)
	last := made[len(made)-1]
	require.Equal(t, synced{path: volumes, staged: false, mode: last.mode}, last, "the directory the volume was renamed into is synced after it")

	before := map[string]fs.FileMode{}
	for _, one := range made[:len(made)-1] {
		require.True(t, one.staged, "%s was synced after the rename", one.path)
		rel, err := filepath.Rel(staging, one.path)
		require.NoError(t, err)
		before[rel] = one.mode
	}
	names := slices.Sorted(maps.Keys(before))
	require.Equal(t, []string{".", "closed", "closed/rows", "d", "d/rows", "top"}, names)
	require.Equal(t, fs.FileMode(0o500), before["closed"], "the closed directory was last synced before it was closed")
}

func TestARestoreThatCannotBeMadeDurableIsNotServed(t *testing.T) {
	archive := entries(t,
		tar.Header{Name: "closed", Typeflag: tar.TypeDir, Mode: 0o500},
		tar.Header{Name: "closed/rows", Typeflag: tar.TypeReg, Mode: 0o640},
	)

	// The file, ".", "closed", "closed" again once it is closed, then the
	// volumes directory once the rename is done.
	const syncs = 5
	for fail := 1; fail <= syncs; fail++ {
		root := t.TempDir()
		v, _ := restorer(t, root, archive)
		calls := 0
		volume.SyncWith(v, func(file *os.File) error {
			if calls++; calls == fail {
				return errors.New("the disk went away")
			}
			return file.Sync()
		})

		_, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
		require.ErrorContains(t, err, "the disk went away", "sync %d", fail)

		left, err := os.ReadDir(filepath.Join(root, "volumes"))
		require.NoError(t, err)
		if fail < syncs {
			require.Empty(t, left, "sync %d failed and left something behind", fail)
			continue
		}
		dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
		require.NoError(t, err)
		os.Chmod(filepath.Join(dir, "closed"), 0o700)
		require.FileExists(t, filepath.Join(dir, "closed", "rows"))
	}
}
