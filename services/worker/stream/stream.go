// Package stream backs `taubyte:stream/publisher`, `taubyte:bus/publisher` and the
// browser's end of a bus. A bus is gossipsub, one topic joined once and kept per
// handle (this file); a stream is the engine it names (publisher.go), delivered to
// the workloads that subscribe.
package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	gateway "github.com/taubyte/tau/services/gateway"
)

// Gossip joins a topic.
//
// DECLARED HERE, THOUGH services/discovery SPELLS THE SAME METHOD, because this is
// the consumer: a publisher has no business importing a service to name the
// gossip it publishes over. Anything that can join a topic satisfies both.
type Gossip interface {
	Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error)
}

// Bus is a bus's backing: gossipsub, one joined topic per handle. Construct it with
// NewBus.
type Bus struct {
	gossip Gossip
	cloud  string

	mu     sync.Mutex
	topics map[string]*pubsub.Topic
}

var _ wasmtier.Streams = (*Bus)(nil)

// ErrNoPubSub is a node asked to publish with no gossip to publish over. It is
// a composition fault: a node running a worker without pubsub can run nothing
// that talks to anything else.
var ErrNoPubSub = errors.New("stream: no pubsub to publish over")

// NewBus wires the backing. gossip may be nil: a workload reaching a bus is then
// refused on its first message, not at start. The cloud may be empty on a
// development node.
func NewBus(gossip Gossip, cloud string) *Bus {
	return &Bus{gossip: gossip, cloud: cloud}
}

func (s *Bus) Publish(ctx context.Context, grant invoke.Grant, message []byte) error {
	topic, err := s.topic(grant.Handle)
	if err != nil {
		return err
	}
	return topic.Publish(ctx, message)
}

func (s *Bus) topic(handle string) (*pubsub.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if held, ok := s.topics[handle]; ok {
		return held, nil
	}
	if s.gossip == nil {
		return nil, ErrNoPubSub
	}

	// THE CLOUD IS IN THE NAME, above the handle's own project and
	// deployment. Two clouds sharing a machine, or bridged by a stray peer,
	// must not deliver each other's messages — which is the same reason
	// stats.NewBus scopes its topic by cloud.
	joined, err := s.gossip.Join(Topic(s.cloud, handle))
	if err != nil {
		return nil, fmt.Errorf("stream: joining the topic for %s: %w", handle, err)
	}

	if s.topics == nil {
		s.topics = map[string]*pubsub.Topic{}
	}
	s.topics[handle] = joined
	return joined, nil
}

// Topic is what goes on the wire: the kind, cloud and the handle (which carries
// project, deployment and id), never the author's label. Exported as a contract:
// anything attaching a browser must listen on exactly this.
func Topic(cloud, handle string) string {
	return strings.Join([]string{"/tau", specs.KindBus, cloud, handle}, "/")
}

// subscribe opens one subscription to the topic a handle names.
//
// ONE PER CALLER AND NOT ONE PER TOPIC. gossipsub delivers to every
// subscription on a joined topic, so a browser attaching gets its own and
// cancelling it touches nobody else's — which is what lets the topic stay
// joined for the life of the node while attachments come and go.
func (s *Bus) subscribe(handle string) (*pubsub.Topic, *pubsub.Subscription, error) {
	topic, err := s.topic(handle)
	if err != nil {
		return nil, nil, err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return nil, nil, fmt.Errorf("stream: subscribing to the topic for %s: %w", handle, err)
	}
	return topic, sub, nil
}

// Close releases the topics this node joined.
func (s *Bus) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, topic := range s.topics {
		topic.Close()
	}
	s.topics = nil
	return nil
}

// Buses is a browser's view of the same topics the wasm tier publishes to.
//
// IT HOLDS THE SAME *Bus THE TIER DOES, so the topic is joined once. Two
// joins of one topic is two meshes for it, and the browser would be on the one
// the workload is not.
type Buses struct{ streams *Bus }

var _ gateway.Buses = (*Buses)(nil)

// NewBuses wires the browser's end onto a backing.
//
// streams MAY NOT BE NIL, because there is nothing for an attachment to be a
// view OF — and a node with no gossip at all still has one: it refuses by name,
// which is what tells a browser that this cloud does not serve that bus.
func NewBuses(streams *Bus) (*Buses, error) {
	if streams == nil {
		return nil, errors.New("stream: no backing for a bus to be attached to")
	}
	return &Buses{streams: streams}, nil
}

func (b *Buses) Attach(ctx context.Context, handle string) (gateway.Attachment, error) {
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
