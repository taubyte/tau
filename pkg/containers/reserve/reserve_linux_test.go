package reserve

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestARunOfZerosIsAHole(t *testing.T) {
	file := made(t)
	content := append(make([]byte, 64*Chunk), "end"...)
	_, err := Floor{}.Copy(file, bytes.NewReader(content))
	require.NoError(t, err)

	stat, err := file.Stat()
	require.NoError(t, err)
	require.Equal(t, int64(len(content)), stat.Size())
	allocated := stat.Sys().(*syscall.Stat_t).Blocks * 512
	require.Less(t, allocated, int64(Chunk), "64MiB of zeros took %d bytes on disk", allocated)
}
