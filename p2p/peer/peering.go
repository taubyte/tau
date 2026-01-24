package peer

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// Seed the random number generator.
//
// We don't need good randomness, but we do need randomness.
const (
	// maxBackoff is the maximum time between reconnect attempts.
	maxBackoff = 5 * time.Minute
	// The backoff will be cut off when we get within 10% of the actual max.
	// If we go over the max, we'll adjust the delay down to a random value
	// between 90-100% of the max backoff.
	maxBackoffJitter = 10 // %
	connmgrTag       = "peering"
	// This needs to be sufficient to prevent two sides from simultaneously
	// dialing.
	initialDelay = 5 * time.Second
)

type state int

const (
	stateInit state = iota
	stateRunning
	stateStopped
)

// peerHandler keeps track of all state related to a specific "peering" peer.
type peerHandler struct {
	peer   peer.ID
	node   *node
	host   host.Host
	ctx    context.Context
	cancel context.CancelFunc

	mu             sync.Mutex
	addrs          []multiaddr.Multiaddr
	reconnectTimer *time.Timer

	nextDelay time.Duration
}

// setAddrs sets the addresses for this peer.
func (ph *peerHandler) setAddrs(addrs []multiaddr.Multiaddr) {
	// Not strictly necessary, but it helps to not trust the calling code.
	addrCopy := make([]multiaddr.Multiaddr, len(addrs))
	copy(addrCopy, addrs)

	ph.mu.Lock()
	defer ph.mu.Unlock()
	ph.addrs = addrCopy
}

// getAddrs returns a shared slice of addresses for this peer. Do not modify.
func (ph *peerHandler) getAddrs() []multiaddr.Multiaddr {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	return ph.addrs
}

// stop permanently stops the peer handler.
func (ph *peerHandler) stop() {
	ph.cancel()

	ph.mu.Lock()
	defer ph.mu.Unlock()
	if ph.reconnectTimer != nil {
		ph.reconnectTimer.Stop()
		ph.reconnectTimer = nil
	}
}

func (ph *peerHandler) nextBackoff() time.Duration {
	if ph.nextDelay < maxBackoff {
		ph.nextDelay += ph.nextDelay/2 + time.Duration(rand.Int63n(int64(ph.nextDelay)))
	}

	// If we've gone over the max backoff, reduce it under the max.
	if ph.nextDelay > maxBackoff {
		ph.nextDelay = maxBackoff
		// randomize the backoff a bit (10%).
		ph.nextDelay -= time.Duration(rand.Int63n(int64(maxBackoff) * maxBackoffJitter / 100))
	}

	return ph.nextDelay
}

func (ph *peerHandler) reconnect() {
	// Try connecting
	addrs := ph.getAddrs()

	err := ph.host.Connect(ph.ctx, peer.AddrInfo{ID: ph.peer, Addrs: addrs})

	if err != nil {
		// Ok, we failed. Set up a timer for retry if we're still disconnected.
		ph.mu.Lock()
		if ph.reconnectTimer == nil && ph.host.Network().Connectedness(ph.peer) != network.Connected {
			// Connection failed and we're still disconnected - schedule a retry with backoff
			ph.reconnectTimer = time.AfterFunc(ph.nextBackoff(), ph.reconnect)
		} else if ph.reconnectTimer != nil {
			// Timer already exists, reset it with new backoff
			ph.reconnectTimer.Reset(ph.nextBackoff())
		}
		// If reconnectTimer is nil and we're connected, connection was established
		// by someone else, so we don't need to do anything.
		ph.mu.Unlock()
	}

	// Always call this. We could have connected since we processed the
	// error.
	ph.stopIfConnected()
}

func (ph *peerHandler) stopIfConnected() {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	if ph.reconnectTimer != nil && ph.host.Network().Connectedness(ph.peer) == network.Connected {
		ph.reconnectTimer.Stop()
		ph.reconnectTimer = nil
		ph.nextDelay = initialDelay
	}
}

// startIfDisconnected is the inverse of stopIfConnected.
func (ph *peerHandler) startIfDisconnected() {
	ph.mu.Lock()
	shouldReconnect := ph.reconnectTimer == nil && ph.host.Network().Connectedness(ph.peer) != network.Connected
	ph.mu.Unlock()

	if shouldReconnect {
		// Try to connect immediately first, then use backoff for retries
		// This avoids the initial 5 second delay when adding a new peer
		ph.reconnect()
	}
}

// PeeringService maintains connections to specified peers, reconnecting on
// disconnect with a back-off.
type peeringService struct {
	node *node
	host host.Host

	mu    sync.RWMutex
	peers map[peer.ID]*peerHandler
	state state
}

// NewPeeringService constructs a new peering service. Peers can be added and
// removed immediately, but connections won't be formed until `Start` is called.
func NewPeeringService(node *node) PeeringService {
	return &peeringService{node: node, host: node.host, peers: make(map[peer.ID]*peerHandler)}
}

// Start starts the peering service, connecting and maintaining connections to
// all registered peers. It returns an error if the service has already been
// stopped.
func (ps *peeringService) Start() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	switch ps.state {
	case stateInit:
		logger.Info("Peering - starting")
	case stateRunning:
		return nil
	case stateStopped:
		return errors.New("already stopped")
	}
	ps.host.Network().Notify((*netNotifee)(ps))
	ps.state = stateRunning
	for _, handler := range ps.peers {
		go handler.startIfDisconnected()
	}
	return nil
}

// Stop stops the peering service.
func (ps *peeringService) Stop() error {
	ps.host.Network().StopNotify((*netNotifee)(ps))

	ps.mu.Lock()
	defer ps.mu.Unlock()

	switch ps.state {
	case stateInit, stateRunning:
		logger.Info("Peering - stopping")
		for _, handler := range ps.peers {
			handler.stop()
		}
		ps.state = stateStopped
	}
	return nil
}

// AddPeer adds a peer to the peering service. This function may be safely
// called at any time: before the service is started, while running, or after it
// stops.
//
// Add peer may also be called multiple times for the same peer. The new
// addresses will replace the old.
func (ps *peeringService) AddPeer(info peer.AddrInfo) {
	var (
		handler     *peerHandler
		shouldStart bool
		isConnected bool
	)

	ps.mu.Lock()
	if existingHandler, ok := ps.peers[info.ID]; ok {
		ps.mu.Unlock()
		logger.Info("updating addresses", "peer", info.ID, "addrs", info.Addrs)
		existingHandler.setAddrs(info.Addrs)
		return
	}

	logger.Info("peer added", "peer", info.ID, "addrs", info.Addrs)

	ps.host.ConnManager().Protect(info.ID, connmgrTag)

	handler = &peerHandler{
		node:      ps.node,
		host:      ps.host,
		peer:      info.ID,
		addrs:     info.Addrs,
		nextDelay: initialDelay,
	}
	handler.ctx, handler.cancel = context.WithCancel(context.Background())
	ps.peers[info.ID] = handler

	shouldStart = ps.state == stateRunning
	if shouldStart {
		isConnected = ps.host.Network().Connectedness(info.ID) == network.Connected
	}
	if ps.state == stateStopped {
		handler.cancel()
	}
	ps.mu.Unlock()

	if shouldStart {
		// Check if already connected - if so, ensure handler is set up properly
		if isConnected {
			go handler.stopIfConnected()
		} else {
			// Not connected - start reconnection process
			go handler.startIfDisconnected()
		}
	}
}

// RemovePeer removes a peer from the peering service. This function may be
// safely called at any time: before the service is started, while running, or
// after it stops.
func (ps *peeringService) RemovePeer(id peer.ID) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if handler, ok := ps.peers[id]; ok {
		logger.Info("peer removed", "peer", id)
		ps.host.ConnManager().Unprotect(id, connmgrTag)

		handler.stop()
		delete(ps.peers, id)
	}
}

type netNotifee peeringService

func (nn *netNotifee) Connected(_ network.Network, c network.Conn) {
	ps := (*peeringService)(nn)

	p := c.RemotePeer()

	ps.mu.RLock()
	handler, ok := ps.peers[p]
	ps.mu.RUnlock()

	if ok {
		// use a goroutine to avoid blocking events.
		go handler.stopIfConnected()
	}
	// Note: We intentionally don't protect inbound connections from unknown peers.
	// Protecting them without a cleanup mechanism would cause connection manager leaks.
	// The connection manager will handle these connections appropriately.
}

func (nn *netNotifee) Disconnected(_ network.Network, c network.Conn) {
	ps := (*peeringService)(nn)

	p := c.RemotePeer()

	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if handler, ok := ps.peers[p]; ok {
		// use a goroutine to avoid blocking events.
		go handler.startIfDisconnected()
	}
}

func (nn *netNotifee) OpenedStream(network.Network, network.Stream)     {}
func (nn *netNotifee) ClosedStream(network.Network, network.Stream)     {}
func (nn *netNotifee) Listen(network.Network, multiaddr.Multiaddr)      {}
func (nn *netNotifee) ListenClose(network.Network, multiaddr.Multiaddr) {}
