package kvdb

import (
	"context"
	"strings"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// PubSubBroadcaster implements a Broadcaster using libp2p PubSub.
type PubSubBroadcaster struct {
	lock  sync.Mutex
	ctx   context.Context
	psub  *pubsub.PubSub
	topic *pubsub.Topic
	subs  *pubsub.Subscription
}

// broadcasterKey identifies a cached broadcaster. It is keyed by the PubSub
// instance as well as the topic string: the cache exists only to avoid joining
// the same topic twice on the same PubSub, which is an inherently per-PubSub
// concern. Keying by topic alone aliased broadcasters across DIFFERENT nodes
// that happen to share a topic name (e.g. every patrick node uses
// "patrick/broadcast"). Because tau runs many nodes in one process under Dream,
// a live node could be handed a broadcaster bound to another (already-closed)
// node's context and PubSub — so its CRDT rebroadcasts returned the dead node's
// "context canceled" and its topic was closed underneath it. Including the
// PubSub pointer in the key isolates each node's broadcaster.
type broadcasterKey struct {
	psub  *pubsub.PubSub
	topic string
}

var (
	broadcasters     = make(map[broadcasterKey]*PubSubBroadcaster)
	broadcastersLock sync.Mutex
)

func registerTopic(key broadcasterKey, b *PubSubBroadcaster) {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	broadcasters[key] = b
}

// unregisterTopic removes b from the cache only if it is still the registered
// broadcaster for key. The identity check keeps a late-firing cleanup goroutine
// from evicting a newer broadcaster that reclaimed the same key.
func unregisterTopic(key broadcasterKey, b *PubSubBroadcaster) {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	if broadcasters[key] == b {
		delete(broadcasters, key)
	}
}

func getTopic(key broadcasterKey) *PubSubBroadcaster {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	return broadcasters[key]
}

// NewPubSubBroadcaster returns a new broadcaster using the given PubSub and
// a topic to subscribe/broadcast to. The given context can be used to cancel
// the broadcaster.
// Please register any topic validators before creating the Broadcaster.
//
// The broadcaster can be shut down by cancelling the given context.
// This must be done before Closing the Datastore, otherwise things
// may hang.
func NewPubSubBroadcaster(ctx context.Context, psub *pubsub.PubSub, topic string) (b *PubSubBroadcaster, err error) {
	key := broadcasterKey{psub: psub, topic: topic}

	if b = getTopic(key); b != nil {
		return b, nil
	}

	b = &PubSubBroadcaster{
		ctx:  ctx,
		psub: psub,
	}

	b.topic, err = psub.Join(topic)
	if err != nil {
		return nil, err
	}

	registerTopic(key, b)

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
		unregisterTopic(key, b)
	}()

	return
}

func (pbc *PubSubBroadcaster) ensureSubscribed() (err error) {
	pbc.lock.Lock()
	defer pbc.lock.Unlock()

	pbc.subs, err = pbc.topic.Subscribe()

	return
}

// Broadcast publishes some data.
//
// libp2p Topic.Publish can wedge in the pubsub validation pipeline when the
// router is tearing down (e.g. a killed node) — it stops observing ctx once past
// its initial check. Publish detached and return as soon as the broadcaster is
// shutting down, so a CRDT rebroadcast can never block Datastore.Close()'s
// wg.Wait (and thus service/universe teardown). The abandoned Publish unwinds on
// its own when the pubsub finishes closing.
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

// Next returns published data.
func (pbc *PubSubBroadcaster) Next(ctx context.Context) ([]byte, error) {
	for try := 3; try > 0; try-- {
		msg, err := pbc.next(ctx)
		if err != ErrNoMoreBroadcast {
			return msg, err
		}

		// Don't re-subscribe a broadcaster that's shutting down: ensureSubscribed
		// would block on the lock the cleanup goroutine holds while it closes the
		// topic, hanging this reader (handleNext) and so Close's wg.Wait.
		select {
		case <-pbc.ctx.Done():
			return nil, ErrNoMoreBroadcast
		case <-ctx.Done():
			return nil, ErrNoMoreBroadcast
		default:
		}

		// try again
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
