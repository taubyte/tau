package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	gateway "github.com/taubyte/tau/services/v2/gateway"
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
//
// ONE TYPE SERVES BOTH KINDS, with `kind` in the topic. A stream and a bus send
// the same way and keep different things — a subscriber gets every message on a
// stream, and a bus keeps nothing at all — so what differs is who is listening
// and for how long, not how a workload publishes.
type streams struct {
	node *Node
	kind string

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
	joined, err := s.node.opts.pubsub.Join(topicFor(s.kind, s.node.cfg.Cloud, handle))
	if err != nil {
		return nil, fmt.Errorf("node: joining the topic for %s: %w", handle, err)
	}

	if s.topics == nil {
		s.topics = map[string]*pubsub.Topic{}
	}
	s.topics[handle] = joined
	return joined, nil
}

// topicFor is what goes on the wire.
//
// IT IS NOT THE LABEL AN AUTHOR WROTE. The handle already carries the project,
// the deployment and the resource's id, so two projects using the name `orders`
// never meet — which is the property the vocabulary states and this is where it
// is kept.
//
// THE KIND IS IN IT because a stream and a bus are different resources that may
// share an id space; a project with both would otherwise cross them.
func topicFor(kind, cloud, handle string) string {
	return strings.Join([]string{"/tau", kind, cloud, handle}, "/")
}

// streamTopic is topicFor on the stream kind, which is what Node.StreamTopic
// exposes.
func streamTopic(cloud, handle string) string { return topicFor("stream", cloud, handle) }

// subscribe opens one subscription to the topic a handle names.
//
// ONE PER CALLER AND NOT ONE PER TOPIC. gossipsub delivers to every
// subscription on a joined topic, so a browser attaching gets its own and
// cancelling it touches nobody else's — which is what lets the topic stay
// joined for the life of the node while attachments come and go.
func (s *streams) subscribe(handle string) (*pubsub.Topic, *pubsub.Subscription, error) {
	topic, err := s.topic(handle)
	if err != nil {
		return nil, nil, err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return nil, nil, fmt.Errorf("node: subscribing to the topic for %s: %w", handle, err)
	}
	return topic, sub, nil
}

// buses is the node's gateway.Buses: a browser's view of the same topics the
// wasm tier publishes to.
//
// IT HOLDS THE SAME *streams THE TIER DOES, so the topic is joined once. Two
// joins of one topic is two meshes for it, and the browser would be on the one
// the workload is not.
type buses struct{ streams *streams }

var _ gateway.Buses = (*buses)(nil)

func (b *buses) Attach(ctx context.Context, handle string) (gateway.Attachment, error) {
	topic, sub, err := b.streams.subscribe(handle)
	if err != nil {
		return nil, err
	}
	return &attachment{topic: topic, sub: sub}, nil
}

// attachment is one browser on one bus.
type attachment struct {
	topic *pubsub.Topic
	sub   *pubsub.Subscription
}

var _ gateway.Attachment = (*attachment)(nil)

func (a *attachment) Next(ctx context.Context) ([]byte, error) {
	msg, err := a.sub.Next(ctx)
	if err != nil {
		return nil, err
	}
	// THE PAYLOAD AND NOTHING ELSE. A workload publishing to this topic writes
	// the bytes it was given, so a browser has to receive exactly those — an
	// envelope here would be a second wire format for one topic.
	return msg.Data, nil
}

func (a *attachment) Publish(ctx context.Context, message []byte) error {
	return a.topic.Publish(ctx, message)
}

// Close cancels this attachment's subscription and leaves the topic joined, for
// the same reason the topic was joined once.
func (a *attachment) Close() error {
	a.sub.Cancel()
	return nil
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
