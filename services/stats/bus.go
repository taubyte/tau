package stats

import (
	"context"
	"errors"
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// bus.go is the production Bus: one libp2p gossipsub topic.
//
// It is here rather than in the composition root because it is the ONE
// implementation of an interface this package declares, and it is four methods
// long. A caller composing a node should be handing over a bus, not writing one.
//
// WHY GOSSIPSUB AND NOT A DIRECT DIAL. A report goes to every node, every
// interval, and nobody acknowledges it. That is a broadcast, and gossipsub is
// the broadcast this swarm already runs — pkg/kvdb uses it for the same reason.
// N direct dials per report per node would be N² connections carrying data no
// receiver has to confirm.

// TopicOf is the gossipsub topic stats gossips on for one cloud.
//
// IT IS SCOPED, and that is not decoration: two clouds sharing a machine, or
// two clouds bridged by a stray peer, would otherwise pool their load figures
// and route each other's traffic. The scope is whatever the composer says
// identifies this cloud.
func TopicOf(cloud string) string {
	if cloud == "" {
		cloud = "default"
	}
	return "/tau/stats/v2/" + cloud
}

// PubSub is the slice of a libp2p pubsub this bus needs, declared here so the
// bus itself is testable without a swarm.
type PubSub interface {
	Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error)
}

// NewBus joins the topic and returns a Bus over it.
//
// The topic is joined once, here, rather than per publish: joining is what
// starts the gossip machinery for it, and a bus that rejoined on every report
// would churn the mesh it depends on.
func NewBus(ps PubSub, cloud string) (Bus, error) {
	if ps == nil {
		return nil, errors.New("stats: no pubsub to gossip over")
	}
	topic, err := ps.Join(TopicOf(cloud))
	if err != nil {
		return nil, fmt.Errorf("stats: joining %s: %w", TopicOf(cloud), err)
	}
	return &gossipBus{topic: topic}, nil
}

type gossipBus struct{ topic *pubsub.Topic }

func (b *gossipBus) Publish(ctx context.Context, data []byte) error {
	return b.topic.Publish(ctx, data)
}

// Subscribe reads the topic until ctx ends.
//
// THE READER GOROUTINE OWNS THE SUBSCRIPTION AND CLOSES THE CHANNEL. A caller
// that saw the channel close knows the subscription is finished; one that had
// to cancel and then also unsubscribe would have two ways to get it wrong.
//
// A message this node published comes back on its own subscription, and that is
// fine: the view folds its own row in directly as well, and observing the same
// report twice is idempotent — same peer, same incarnation, same numbers.
func (b *gossipBus) Subscribe(ctx context.Context) (<-chan []byte, error) {
	sub, err := b.topic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("stats: subscribing to %s: %w", b.topic.String(), err)
	}

	out := make(chan []byte, 64)
	go func() {
		defer close(out)
		defer sub.Cancel()
		for {
			msg, err := sub.Next(ctx)
			if err != nil {
				// ctx ended, or the subscription was cancelled. Either way
				// there is nothing left to read.
				return
			}
			select {
			case out <- msg.Data:
			case <-ctx.Done():
				return
			default:
				// A SLOW READER DROPS A REPORT RATHER THAN STALLING THE MESH.
				// Blocking here would back up gossipsub's delivery for every
				// topic on this host, and the next report is along in one
				// interval — losing one is what the freshness window already
				// tolerates.
			}
		}
	}()
	return out, nil
}
