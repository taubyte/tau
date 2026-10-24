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

// TestASPARSEFileStaysSparseWhereItIsRestored. The checkpoint holds the zeros a
// sparse file reads as; written back as zeros they would take the space the
// file never took where it came from.
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
