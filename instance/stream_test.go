package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance/stream"
)

// Only the wiring: which gossip and cloud a backing was built over, and that a bus has
// one backing.

func TestTheNodeExposesBothTopics(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery}})

	require.Equal(t, stream.Topic(stream.Bus, n.cfg.Cloud, "h"), n.BusTopic("h"))
	require.Equal(t, stream.Topic(stream.Stream, n.cfg.Cloud, "h"), n.StreamTopic("h"))
	require.NotEqual(t, n.BusTopic("h"), n.StreamTopic("h"))
}

func TestOneBusBackingIsSharedByBothConsumers(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery, Gateway}})

	first, err := n.busStreams()
	require.NoError(t, err)
	again, err := n.busStreams()
	require.NoError(t, err)

	require.Same(t, first, again)
	require.Same(t, n.bus, first, "and it is the one the composition kept")
}
