package volume

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/reserve"
)

type noStore struct{}

func (noStore) Store() (Rows, error) { return nil, os.ErrNotExist }

type quiet struct{}

func (quiet) Infof(string, ...any) {}
func (quiet) Warnf(string, ...any) {}

// TestANODESRestoresKeepItsReserve: what New wires is the node's reserve.
func TestANodesRestoresKeepItsReserve(t *testing.T) {
	v, err := New(t.TempDir(), nil, noStore{}, quiet{})
	require.NoError(t, err)
	require.Equal(t, reserve.Default, v.floor)
}
