package instance

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
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

func TestABackingOverNoPubSubRefusesByName(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	n, err := New(Config{Services: []string{Worker}},
		WithHost(newHost(t, mn)), WithDatabases(&memFactory{}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	publisher, err := n.streamBacking(stream.Stream)
	require.NoError(t, err)

	err = publisher.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}
