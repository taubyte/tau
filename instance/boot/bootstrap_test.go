package boot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrapAddressesNameAPeer(t *testing.T) {
	_, err := Start(t.Context(), admit(t, File{
		Root:      t.TempDir(),
		Bootstrap: []string{"/ip4/127.0.0.1/tcp/4001"},
	}))
	require.ErrorContains(t, err, "bootstrap")

	_, err = Start(t.Context(), admit(t, File{
		Root:      t.TempDir(),
		Bootstrap: []string{"not-an-address"},
	}))
	require.ErrorContains(t, err, "bootstrap")
}
