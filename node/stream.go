package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// stream.go backs `taubyte:handler/stream` on this node.
//
// # A topic is joined once and kept
//
// Joining is what starts the gossip machinery for a topic, so a publisher that
// rejoined per message would churn the mesh it depends on — the same reason
// services/stats' bus joins once. So topics are kept by handle, and the handle
// is already scoped to a project and a deployment, which is what keeps two
// projects publishing to a stream they both call `orders` apart.
//
// # What it does not do
//
// SUBSCRIBE. The vocabulary's other verb is a DELIVERY — the platform invokes a
// workload when a message arrives — so it is a trigger and not something a
// guest calls. Nothing here dispatches one yet; the op check refuses a resource
// that asked for it, which says what the platform does not do rather than
// leaving a subscriber waiting.

// streams is the node's Streams: gossipsub, one joined topic per handle.
type streams struct {
	node *Node

	mu     sync.Mutex
	topics map[string]*pubsub.Topic
}

var _ wasmtier.Streams = (*streams)(nil)

// ErrNoPubSubForStreams is a node asked to publish with no gossip to publish
// over. It is a composition fault: a node running a worker without pubsub can
// run nothing that talks to anything else.
var ErrNoPubSubForStreams = errors.New("node: no pubsub to publish a stream over")

// Publish sends one message to the topic a handle names.
func (s *streams) Publish(ctx context.Context, handle string, message []byte) error {
	topic, err := s.topic(handle)
	if err != nil {
		return err
	}
	return topic.Publish(ctx, message)
}

func (s *streams) topic(handle string) (*pubsub.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if held, ok := s.topics[handle]; ok {
		return held, nil
	}
	if s.node.opts.pubsub == nil {
		return nil, ErrNoPubSubForStreams
	}

	// THE CLOUD IS IN THE NAME, above the handle's own project and
	// deployment. Two clouds sharing a machine, or bridged by a stray peer,
	// must not deliver each other's messages — which is the same reason
	// stats.NewBus scopes its topic by cloud.
	joined, err := s.node.opts.pubsub.Join(streamTopic(s.node.cfg.Cloud, handle))
	if err != nil {
		return nil, fmt.Errorf("node: joining the topic for %s: %w", handle, err)
	}

	if s.topics == nil {
		s.topics = map[string]*pubsub.Topic{}
	}
	s.topics[handle] = joined
	return joined, nil
}

// streamTopic is what goes on the wire.
//
// IT IS NOT THE LABEL AN AUTHOR WROTE. The handle already carries the project,
// the deployment and the stream's id, so two projects using the name `orders`
// never meet — which is the property the vocabulary states and this is where it
// is kept.
func streamTopic(cloud, handle string) string {
	return strings.Join([]string{"/tau/stream", cloud, handle}, "/")
}

// Close releases the topics this node joined.
func (s *streams) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, topic := range s.topics {
		topic.Close()
	}
	s.topics = nil
	return nil
}
