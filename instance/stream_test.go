package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/worker/stream"
)

// Only the wiring: which gossip and cloud the bus was built over, and that it has one
// backing.

func TestTheNodeExposesTheBusTopic(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery}})
	require.Equal(t, stream.Topic(n.cfg.Cloud, "h"), n.BusTopic("h"))
}

func TestOneBusBackingIsSharedByBothConsumers(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery, Gateway}})

	first := n.busStreams()
	require.Same(t, first, n.busStreams())
	require.Same(t, n.bus, first, "and it is the one the composition kept")
}
