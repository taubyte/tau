package kvdb

import (
	"context"
	"strings"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/taubyte/tau/p2p/peer"
)

type PubSubBroadcaster struct {
	lock  sync.Mutex
	ctx   context.Context
	psub  *pubsub.PubSub
	topic *pubsub.Topic
	subs  *pubsub.Subscription
}

// broadcasters is a node's broadcaster per topic: a topic is joined once on a
// PubSub, so every database on the node that broadcasts on it shares one.
type broadcasters struct {
	sync.Mutex
	on map[string]*PubSubBroadcaster
}

// broadcastersKey is what a node keeps its broadcasters under.
type broadcastersKey struct{}

func (bs *broadcasters) register(topic string, b *PubSubBroadcaster) {
	bs.Lock()
	defer bs.Unlock()
	if bs.on == nil {
		bs.on = map[string]*PubSubBroadcaster{}
	}
	bs.on[topic] = b
}

// The identity check keeps a late cleanup from evicting a newer broadcaster
// under the same topic.
func (bs *broadcasters) unregister(topic string, b *PubSubBroadcaster) {
	bs.Lock()
	defer bs.Unlock()
	if bs.on[topic] == b {
		delete(bs.on, topic)
	}
}

func (bs *broadcasters) get(topic string) *PubSubBroadcaster {
	bs.Lock()
	defer bs.Unlock()
	return bs.on[topic]
}

// Register topic validators first. Cancel ctx to shut it down, before closing
// the Datastore, or Close may hang.
func NewPubSubBroadcaster(ctx context.Context, node peer.Node, topic string) (b *PubSubBroadcaster, err error) {
	on := peer.Kept[broadcasters](node, broadcastersKey{})
	if b = on.get(topic); b != nil {
		return b, nil
	}

	psub := node.Messaging()
	b = &PubSubBroadcaster{
		ctx:  ctx,
		psub: psub,
	}

	b.topic, err = psub.Join(topic)
	if err != nil {
		return nil, err
	}

	on.register(topic, b)

	if err = b.ensureSubscribed(); err != nil {
		return nil, err
	}

	go func() {
		<-b.ctx.Done()

		b.lock.Lock()
		defer b.lock.Unlock()
		if b.subs != nil {
			b.subs.Cancel()
		}
		b.topic.Close()
		on.unregister(topic, b)
	}()

	return
}

func (pbc *PubSubBroadcaster) ensureSubscribed() (err error) {
	pbc.lock.Lock()
	defer pbc.lock.Unlock()

	pbc.subs, err = pbc.topic.Subscribe()

	return
}

// Publish can wedge in pubsub's validation pipeline while the router tears
// down, ignoring ctx; it runs detached so a rebroadcast never blocks Close.
func (pbc *PubSubBroadcaster) Broadcast(ctx context.Context, data []byte) error {
	errc := make(chan error, 1)
	go func() { errc <- pbc.topic.Publish(ctx, data) }()
	select {
	case err := <-errc:
		return err
	case <-pbc.ctx.Done():
		return pbc.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (pbc *PubSubBroadcaster) Next(ctx context.Context) ([]byte, error) {
	for try := 3; try > 0; try-- {
		msg, err := pbc.next(ctx)
		if err != ErrNoMoreBroadcast {
			return msg, err
		}

		// A broadcaster shutting down is not resubscribed: ensureSubscribed would
		// block on the lock its cleanup holds, hanging Close.
		select {
		case <-pbc.ctx.Done():
			return nil, ErrNoMoreBroadcast
		case <-ctx.Done():
			return nil, ErrNoMoreBroadcast
		default:
		}

		if pbc.ensureSubscribed() != nil {
			break
		}
	}

	return nil, ErrNoMoreBroadcast
}

func (pbc *PubSubBroadcaster) next(ctx context.Context) ([]byte, error) {
	var msg *pubsub.Message
	var err error

	select {
	case <-pbc.ctx.Done():
		return nil, ErrNoMoreBroadcast
	case <-ctx.Done():
		return nil, ErrNoMoreBroadcast
	default:
	}

	pbc.lock.Lock()
	defer pbc.lock.Unlock()

	if pbc.subs == nil {
		return nil, ErrNoMoreBroadcast
	}

	msg, err = pbc.subs.Next(pbc.ctx)
	if err != nil {
		if strings.Contains(err.Error(), "subscription cancelled") ||
			strings.Contains(err.Error(), "context") {
			return nil, ErrNoMoreBroadcast
		}
		return nil, err
	}

	return msg.GetData(), nil
}
