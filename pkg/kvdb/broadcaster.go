package kvdb

import (
	"context"
	"strings"
	"sync"

	crdt "github.com/ipfs/go-ds-crdt"
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

var (
	broadcasters     = make(map[string]*PubSubBroadcaster)
	broadcastersLock sync.Mutex
)

func registerTopic(topic string, b *PubSubBroadcaster) {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	broadcasters[topic] = b
}

func unregisterTopic(topic string) {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	delete(broadcasters, topic)
}

func getTopic(topic string) *PubSubBroadcaster {
	broadcastersLock.Lock()
	defer broadcastersLock.Unlock()
	return broadcasters[topic]
}

// NewPubSubBroadcaster returns a new broadcaster using the given PubSub and
// a topic to subscribe/broadcast to. The given context can be used to cancel
// the broadcaster.
// Please register any topic validators before creating the Broadcaster.
//
// The broadcaster can be shut down by cancelling the given context.
// This must be done before Closing the crdt.Datastore, otherwise things
// may hang.
func NewPubSubBroadcaster(ctx context.Context, psub *pubsub.PubSub, topic string) (b *PubSubBroadcaster, err error) {
	if b = getTopic(topic); b != nil {
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

	registerTopic(topic, b)

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
		unregisterTopic(topic)
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
func (pbc *PubSubBroadcaster) Broadcast(ctx context.Context, data []byte) error {
	return pbc.topic.Publish(ctx, data)
}

// Next returns published data.
func (pbc *PubSubBroadcaster) Next(ctx context.Context) ([]byte, error) {
	for try := 3; try > 0; try-- {
		msg, err := pbc.next(ctx)
		if err != crdt.ErrNoMoreBroadcast {
			return msg, err
		}

		// try again
		if pbc.ensureSubscribed() != nil {
			break
		}
	}

	return nil, crdt.ErrNoMoreBroadcast
}

func (pbc *PubSubBroadcaster) next(ctx context.Context) ([]byte, error) {
	var msg *pubsub.Message
	var err error

	select {
	case <-pbc.ctx.Done():
		return nil, crdt.ErrNoMoreBroadcast
	case <-ctx.Done():
		return nil, crdt.ErrNoMoreBroadcast
	default:
	}

	pbc.lock.Lock()
	defer pbc.lock.Unlock()

	if pbc.subs == nil {
		return nil, crdt.ErrNoMoreBroadcast
	}

	msg, err = pbc.subs.Next(pbc.ctx)
	if err != nil {
		if strings.Contains(err.Error(), "subscription cancelled") ||
			strings.Contains(err.Error(), "context") {
			return nil, crdt.ErrNoMoreBroadcast
		}
		return nil, err
	}

	return msg.GetData(), nil
}
