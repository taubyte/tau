package peer

import (
	"context"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/stretchr/testify/require"
)

func joinedTopics(n *node) int {
	n.topicsMutex.RLock()
	defer n.topicsMutex.RUnlock()
	return len(n.topics)
}

// Pubsub polls discovery every second for every joined topic, so a topic nobody
// uses must be left, and joining it again must still deliver.
func TestPubSubTopicClosesAfterLastSubscriber(t *testing.T) {
	ctx := context.Background()
	n := Mock(ctx).(*node)
	defer n.Close()

	for round := 0; round < 3; round++ {
		subCtx1, cancel1 := context.WithCancel(ctx)
		subCtx2, cancel2 := context.WithCancel(ctx)
		got := make(chan []byte, 4)
		h := func(m *pubsub.Message) { got <- m.Data }
		require.NoError(t, n.PubSubSubscribeContext(subCtx1, "rejoin", h, func(error) {}))
		require.NoError(t, n.PubSubSubscribeContext(subCtx2, "rejoin", h, func(error) {}))
		require.Equal(t, 1, joinedTopics(n))

		require.NoError(t, n.PubSubPublish(ctx, "rejoin", []byte("hi")))
		for i := 0; i < 2; i++ {
			select {
			case d := <-got:
				require.Equal(t, "hi", string(d))
			case <-time.After(5 * time.Second):
				t.Fatal("message not delivered")
			}
		}

		cancel1()
		time.Sleep(200 * time.Millisecond)
		require.Equal(t, 1, joinedTopics(n), "one subscriber left, topic must stay")
		cancel2()
		require.Eventually(t, func() bool { return joinedTopics(n) == 0 }, 5*time.Second, 20*time.Millisecond)
	}

	// publish alone must not leave the topic joined either
	require.NoError(t, n.PubSubPublish(ctx, "publish-only", []byte("x")))
	require.Equal(t, 0, joinedTopics(n))
}
