package volume

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/reserve"
)

type noStore struct{}

func (noStore) Store() (Rows, error) { return nil, os.ErrNotExist }

type quiet struct{}

func (quiet) Infof(string, ...any) {}
func (quiet) Warnf(string, ...any) {}

// TestANODESRestoresKeepItsReserveAndSyncThroughTheFile: what New wires is the
// node's reserve, and the file's own Sync, which fails on a file that is
// already closed where one that synced nothing would not.
func TestANodesRestoresKeepItsReserveAndSyncThroughTheFile(t *testing.T) {
	v, err := New(t.TempDir(), nil, noStore{}, quiet{})
	require.NoError(t, err)
	require.Equal(t, reserve.Default, v.floor)

	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	require.NoError(t, err)
	require.NoError(t, v.sync(file))
	require.NoError(t, file.Close())
	require.ErrorIs(t, v.sync(file), os.ErrClosed)
}
