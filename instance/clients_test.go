package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
)

func TestEveryClientsRetryIsOneSectionOfTheNodesConfig(t *testing.T) {
	mn := peertest.Net(t)
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("clients:\n  retry:\n    first: 1s\n    max: 4s\n    for: 1m\n"), &cfg))
	require.Equal(t, rpc.Retry{First: time.Second, Max: 4 * time.Second, For: time.Minute}, cfg.Clients.Retry)

	n, _, _ := makeNode(t, mn, Config{Services: []string{Stats}, Clients: cfg.Clients})
	require.Equal(t, cfg.Clients.Retry, n.HostOf(Stats).Retry(), "a service on the node is not given the node's retry")
}

func TestARetryNoClientCouldFollowIsRefused(t *testing.T) {
	mn := peertest.Net(t)
	_, err := New(mockPeer(t, mn), rooted(t, Config{Services: []string{Stats}, Clients: Clients{Retry: rpc.Retry{First: -time.Second}}}))
	require.ErrorContains(t, err, "clients")
}
