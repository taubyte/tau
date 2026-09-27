package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// TestANarrowedToolGrantIsRefusedByName — an entry naming some of a server's
// tools is an authorization bound nothing here applies, so it fails closed
// rather than being served as every tool.
func TestANarrowedToolGrantIsRefusedByName(t *testing.T) {
	why := unservable(specs.Reach{Kind: mcpKind, Label: "tools", Ops: []string{"call"}, Tools: []string{"refund"}}, nil)
	require.Contains(t, why, "narrowed to the tools [refund]")

	require.Empty(t, unservable(specs.Reach{Kind: mcpKind, Label: "tools", Ops: []string{"call"}}, nil),
		"an entry on the whole catalogue is what the author asked for, and is served")
}
