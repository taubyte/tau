package kvdb

import (
	"context"
	"strings"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

type PubSubBroadcaster struct {
	lock  sync.Mutex
	ctx   context.Context
	psub  *pubsub.PubSub
	topic *pubsub.Topic
	subs  *pubsub.Subscription
}

// Keyed by PubSub too: tau runs many nodes in one process, and a topic name
// alone handed one node another's closed broadcaster.
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

// The identity check keeps a late cleanup from evicting a newer broadcaster
// under the same key.
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

// Register topic validators first. Cancel ctx to shut it down, before closing
// the Datastore, or Close may hang.
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
