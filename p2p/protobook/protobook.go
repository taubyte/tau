// Package protobook keeps a host's book of what its peers serve true where
// identify leaves it stale.
//
// identify writes a peer's protocols into the peerstore from each message the peer
// sends: the answer on a new connection, and a push each time the peer's protocols
// change. It reads the list, works out what changed and writes the message's list in
// its place, two messages from one peer at once, so the one that lands last stays
// even when it is the older. A peer that began to serve is then left listed as it was
// before, and nothing corrects it until its protocols change again. Every reader of
// the peerstore inherits the list: discovery takes it as the peer not serving.
//
// A Keeper checks each message against the book and against the peer's message
// before it. A protocol they disagree on is asked of the peer, over the connection
// the message came on: a stream negotiated for it, then reset. The answer goes in
// the book, and a change is announced as identify announces one, with
// EvtPeerProtocolsUpdated.
package protobook

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
	msmux "github.com/multiformats/go-multistream"
)

const (
	// askTimeout bounds one negotiation. It is libp2p's own negotiation timeout.
	askTimeout = 10 * time.Second

	// retryInterval is how long an ask that got no answer waits to ask again.
	retryInterval = time.Second

	// eventBuffer is slack for a burst of messages: libp2p blocks an emitter whose
	// subscriber is behind.
	eventBuffer = 128
)

// Keeper holds a host's book to what its peers answer.
type Keeper struct {
	host    host.Host
	updated event.Emitter
	timeout time.Duration
	retry   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// heard is what each connected peer's last message named. It is the loop's own.
	heard map[peer.ID][]protocol.ID

	mu     sync.Mutex
	asking map[question]bool
	asks   sync.WaitGroup
}

// question is one protocol asked of one peer.
type question struct {
	peer     peer.ID
	protocol protocol.ID
}

// Keep keeps h's book until ctx ends or the Keeper is closed.
func Keep(ctx context.Context, h host.Host) (*Keeper, error) {
	return keep(ctx, h, askTimeout, retryInterval)
}

func keep(ctx context.Context, h host.Host, timeout, retry time.Duration) (*Keeper, error) {
	sub, err := h.EventBus().Subscribe(
		[]any{new(event.EvtPeerIdentificationCompleted), new(event.EvtPeerConnectednessChanged)},
		eventbus.BufSize(eventBuffer),
		eventbus.Name("p2p/protobook"),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribing to identify: %w", err)
	}
	updated, err := h.EventBus().Emitter(new(event.EvtPeerProtocolsUpdated))
	if err != nil {
		sub.Close()
		return nil, fmt.Errorf("announcing protocol updates: %w", err)
	}

	k := &Keeper{
		host:    h,
		updated: updated,
		timeout: timeout,
		retry:   retry,
		done:    make(chan struct{}),
		heard:   make(map[peer.ID][]protocol.ID),
		asking:  make(map[question]bool),
	}
	k.ctx, k.cancel = context.WithCancel(ctx)
	go k.run(sub)
	return k, nil
}

// Close stops keeping the book, once every ask under way has ended.
func (k *Keeper) Close() {
	k.cancel()
	<-k.done
	k.asks.Wait()
	k.updated.Close()
}

func (k *Keeper) run(sub event.Subscription) {
	defer close(k.done)
	defer sub.Close()

	for {
		select {
		case <-k.ctx.Done():
			return
		case e, ok := <-sub.Out():
			if !ok {
				return
			}
			switch v := e.(type) {
			case event.EvtPeerIdentificationCompleted:
				k.heardFrom(v.Peer, v.Protocols)
			case event.EvtPeerConnectednessChanged:
				if v.Connectedness != network.Connected {
					delete(k.heard, v.Peer)
				}
			}
		}
	}
}

// heardFrom checks a message from p against the book and against p's message
// before it, and asks p about every protocol they disagree on.
func (k *Keeper) heardFrom(p peer.ID, said []protocol.ID) {
	// A message can be told after the disconnect that ended its connection.
	if k.host.Network().Connectedness(p) != network.Connected {
		delete(k.heard, p)
		return
	}
	before := k.heard[p]
	k.heard[p] = said

	book, err := k.host.Peerstore().GetProtocols(p)
	if err != nil {
		return
	}
	for _, id := range disputed(said, book, before) {
		k.ask(question{peer: p, protocol: id})
	}
}

// disputed is every protocol a message, the book and the message before it do not
// agree on: in the message and not the book or the other way round, or named
// before and not now.
func disputed(said, book, before []protocol.ID) []protocol.ID {
	var out []protocol.ID
	note := func(id protocol.ID) {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, id := range said {
		if !slices.Contains(book, id) {
			note(id)
		}
	}
	for _, ids := range [][]protocol.ID{book, before} {
		for _, id := range ids {
			if !slices.Contains(said, id) {
				note(id)
			}
		}
	}
	return out
}

// ask asks q unless it is being asked already. It is called from the loop only, so
// no ask starts once Close is waiting on them.
func (k *Keeper) ask(q question) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.asking[q] {
		return
	}
	k.asking[q] = true
	k.asks.Add(1)

	go func() {
		defer k.asks.Done()
		serves, answered := k.answer(q)
		k.mu.Lock()
		delete(k.asking, q)
		k.mu.Unlock()
		if answered {
			k.record(q, serves)
		}
	}()
}

// answer asks the peer whether it serves the protocol, again each retry interval
// while it stays connected and has not answered. answered is false when the peer
// went away or the Keeper closed first.
func (k *Keeper) answer(q question) (serves, answered bool) {
	for {
		serves, err := k.negotiate(q)
		if err == nil {
			return serves, true
		}
		if k.host.Network().Connectedness(q.peer) != network.Connected {
			return false, false
		}
		select {
		case <-time.After(k.retry):
		case <-k.ctx.Done():
			return false, false
		}
	}
}

// negotiate opens a stream to the peer over the connection it has and negotiates the
// protocol on it. The stream is the network's, not the host's: the host takes the
// book's word for a protocol the book lists and negotiates nothing.
func (k *Keeper) negotiate(q question) (bool, error) {
	ctx, cancel := context.WithTimeout(network.WithNoDial(k.ctx, "protobook asks a connected peer"), k.timeout)
	defer cancel()

	s, err := k.host.Network().NewStream(ctx, q.peer)
	if err != nil {
		return false, err
	}
	defer s.Reset()
	// A negotiation reads until the peer answers, so it ends with the context.
	stop := context.AfterFunc(ctx, func() { s.Reset() })
	defer stop()

	err = msmux.SelectProtoOrFail(q.protocol, s)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, msmux.ErrNotSupported[protocol.ID]{}):
		return false, nil
	}
	return false, err
}

// record writes the peer's answer into the book, and announces it when the book
// said otherwise.
func (k *Keeper) record(q question, serves bool) {
	book, err := k.host.Peerstore().GetProtocols(q.peer)
	if err != nil {
		return
	}
	listed := slices.Contains(book, q.protocol)
	switch {
	case serves && !listed:
		if err := k.host.Peerstore().AddProtocols(q.peer, q.protocol); err != nil {
			return
		}
		k.updated.Emit(event.EvtPeerProtocolsUpdated{Peer: q.peer, Added: []protocol.ID{q.protocol}})
	case !serves && listed:
		if err := k.host.Peerstore().RemoveProtocols(q.peer, q.protocol); err != nil {
			return
		}
		k.updated.Emit(event.EvtPeerProtocolsUpdated{Peer: q.peer, Removed: []protocol.ID{q.protocol}})
	}
}
