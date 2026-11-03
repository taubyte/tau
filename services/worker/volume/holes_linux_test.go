package volume

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWhereTheFilesystemCannotSayAFileIsDataThroughout(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()

	data, end, err := extent(r, 10, 100)
	require.NoError(t, err)
	require.Equal(t, [2]int64{10, 100}, [2]int64{data, end})
}
