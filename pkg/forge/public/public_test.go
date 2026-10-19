package public

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

func TestThePublicForgesAreBound(t *testing.T) {
	r, err := Registry(forge.Policy{})
	require.NoError(t, err)
	require.Equal(t, []string{"github.com", "gitlab.com"}, r.Hosts())
}
