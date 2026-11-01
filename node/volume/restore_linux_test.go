package volume_test

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/volume"
)

func TestASparseFileStaysSparseWhereItIsRestored(t *testing.T) {
	v, store, index, _ := holding(t)
	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	const size = 16 << 20
	file, err := os.Create(filepath.Join(dir, "sparse"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	_, err = file.WriteAt([]byte("end"), size)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.Equal(t, 1, v.CheckpointAll(t.Context()))

	moved, err := volume.New(t.TempDir(), store, &opening{rows: index}, &said{})
	require.NoError(t, err)
	t.Cleanup(func() { moved.Close() })
	restored, err := moved.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(restored, "sparse"))
	require.NoError(t, err)
	require.Equal(t, int64(size+3), info.Size())
	allocated := info.Sys().(*syscall.Stat_t).Blocks * 512
	require.Less(t, allocated, int64(1<<20), "a %d-byte sparse file took %d bytes where it was restored", size+3, allocated)

	content, err := os.ReadFile(filepath.Join(restored, "sparse"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(append(make([]byte, size), "end"...), content), "the restored file differs")
}

func TestAWalkReadsAFilesDataAndNotItsHoles(t *testing.T) {
	v, store, index, _ := holding(t)
	var read int64
	volume.ReadWith(v, func(file *os.File, p []byte, at int64) (int, error) {
		n, err := file.ReadAt(p, at)
		read += int64(n)
		return n, err
	})
	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	// Data at the start and at the end, and twice between, once off a page
	// boundary.
	const length, run = 128 << 20, 64 << 10
	at := []int64{0, 32 << 20, 64<<20 + 100, length - run}
	file, err := os.Create(filepath.Join(dir, "base"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(length))
	for i, off := range at {
		_, err := file.WriteAt(bytes.Repeat([]byte{byte(i + 1)}, run), off)
		require.NoError(t, err)
	}
	require.NoError(t, file.Close())

	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.GreaterOrEqual(t, read, int64(len(at)*run), "the walk read less than the file holds")
	require.Less(t, read, int64(length/8), "the walk read %d bytes of a %d-byte file holding %d", read, length, len(at)*run)

	moved, err := volume.New(t.TempDir(), store, &opening{rows: index}, &said{})
	require.NoError(t, err)
	t.Cleanup(func() { moved.Close() })
	restored, err := moved.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	back, err := os.Open(filepath.Join(restored, "base"))
	require.NoError(t, err)
	defer back.Close()
	info, err := back.Stat()
	require.NoError(t, err)
	require.Equal(t, int64(length), info.Size())
	require.Less(t, info.Sys().(*syscall.Stat_t).Blocks*512, int64(length/8), "the holes came back as data")
	for i, off := range at {
		got := make([]byte, run)
		_, err := back.ReadAt(got, off)
		require.NoError(t, err)
		require.Equal(t, bytes.Repeat([]byte{byte(i + 1)}, run), got, "the data at %d came back different", off)
	}
}
