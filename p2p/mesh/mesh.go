// Package mesh runs a set of libp2p peers inside one process, on loopback,
// meshed to each other — the network a v2 end-to-end test stands its services
// on.
//
// # Why this and not dream
//
// v1's dream universe does this too, and a great deal more: a registry of v1
// service factories, simples carrying v1 clients, a fixture runner, an HTTP
// api. Importing it for the mesh alone dragged every v1 service package into
// v2's test binaries. What a v2 test needs from it is a handful of peers that
// can find each other, which is all this is.
//
// # What is kept from dream, and why
//
// The three things about a universe's network that were measured rather than
// chosen are kept exactly: a peer's services start before it joins (see Add),
// the canonical-direction dial (see dial), and the bounded WaitForSwarm after
// it. Each is commented where it lives.
// dream's 750ms settle is NOT here, because dream only ever paid it for a
// simple — a client node — and a mesh has none.
package mesh

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/utils"
)

const (
	// listen is every peer's p2p address. Port 0, always: the bind is the
	// allocation, so two fleets in one machine cannot pick the same number.
	listen = "/ip4/127.0.0.1/tcp/0"

	// meshTimeout bounds how long joining the mesh may take, dream's number.
	meshTimeout = 30 * time.Second

	// swarmWait is the bounded wait after meshing. Skipping it — even for the
	// first peer, which pays all of it — lets a service make its first pubsub
	// and DHT moves while isolated, and those failures are backoff-cached for
	// a minute and more, wedging the mesh. Not relaxable.
	swarmWait = time.Second

	// drain is how long Close lets services finish before their peers go.
	drain = 300 * time.Millisecond
)

// Mesh is a set of meshed peers and whatever runs on them.
type Mesh struct {
	ctx    context.Context
	cancel context.CancelFunc

	name     string
	root     string
	swarmKey []byte

	mu      sync.RWMutex
	peers   []peer.Node
	added   int
	closers []io.Closer
}

// New makes an empty mesh. Its name scopes anything that is shared by name —
// a pubsub topic, a cloud — so two fleets in one process do not hear each
// other; it is lowercased, as a cloud name is.
func New(ctx context.Context, name string) (*Mesh, error) {
	name = strings.ToLower(name)

	root, err := os.MkdirTemp("", "mesh-")
	if err != nil {
		return nil, err
	}

	// A PRIVATE NETWORK PER MESH, so a peer of one mesh cannot join another
	// even when both dial the same loopback ports in turn.
	key, err := utils.FormatSwarmKey(utils.GenerateSwarmKeyFromString(root))
	if err != nil {
		os.RemoveAll(root)
		return nil, err
	}

	m := &Mesh{name: name, root: root, swarmKey: key}
	m.ctx, m.cancel = context.WithCancel(ctx)
	return m, nil
}

// Name is the mesh's name, lowercased.
func (m *Mesh) Name() string { return m.name }

// Context ends when the mesh closes. Services started on a mesh run on it.
func (m *Mesh) Context() context.Context { return m.ctx }

// Root is the mesh's own directory; every peer's datastore is under it.
func (m *Mesh) Root() string { return m.root }

// Peers are the mesh's peers, in the order they joined.
func (m *Mesh) Peers() []peer.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]peer.Node(nil), m.peers...)
}

// Add makes a peer, starts what runs on it, and then meshes it with every peer
// already here.
//
// WHAT RUNS ON A PEER STARTS BEFORE IT JOINS, which is the order dream always
// had and the one a fleet is measured against: a CRDT whose node starts after
// it is already connected never fetches the heads it is told about (node/dream's
// TestACacheReplicatesBetweenNodes stays at "a node never reached the project"
// with the order reversed). start may be nil.
func (m *Mesh) Add(start func(peer.Node) error) (peer.Node, error) {
	existing := m.Peers()

	bootstrap := peer.StandAlone()
	if len(existing) > 0 {
		infos := make([]peercore.AddrInfo, 0, len(existing))
		for _, n := range existing {
			infos = append(infos, peercore.AddrInfo{ID: n.ID(), Addrs: n.Peer().Addrs()})
		}
		bootstrap = peer.Bootstrap(infos...)
	}

	m.mu.Lock()
	dir := filepath.Join(m.root, fmt.Sprintf("peer-%d", m.added))
	m.added++
	m.mu.Unlock()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}

	n, err := peer.NewPublic(m.ctx, dir, keypair.NewRaw(), m.swarmKey, []string{listen}, []string{listen}, bootstrap)
	if err != nil {
		return nil, err
	}

	if start != nil {
		if err := start(n); err != nil {
			n.Close()
			return nil, err
		}
	}

	m.dial(n, existing)

	// A peer alone in the mesh times out here, which is fine: the wait is
	// bounded and best-effort, see swarmWait.
	n.WaitForSwarm(swarmWait)

	m.mu.Lock()
	m.peers = append(m.peers, n)
	m.mu.Unlock()
	return n, nil
}

// dial connects the new peer to every existing one.
//
// EACH PAIR IS DIALED ONCE, FROM A CANONICAL SIDE. Concurrent crossed dials
// make both TLS handshakes act as the client and park both sides in the
// swarm's dial backoff, wedging the pair for tens of seconds. A failed dial is
// retried with freshly resolved addresses before falling back to the peering
// service, whose reconnect backoff can leave a mesh half-meshed for a minute.
func (m *Mesh) dial(n peer.Node, existing []peer.Node) {
	ctx, cancel := context.WithTimeout(m.ctx, meshTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for _, other := range existing {
		n0, n1 := n, other
		if n0.ID() > n1.ID() {
			n0, n1 = n1, n0
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				info := peercore.AddrInfo{ID: n1.ID(), Addrs: n1.Peer().Addrs()}
				if len(info.Addrs) > 0 {
					if err := n0.Peer().Connect(ctx, info); err == nil {
						return
					}
				}
				select {
				case <-ctx.Done():
					n0.Peering().AddPeer(info)
					return
				case <-time.After(time.Second):
				}
			}
			n0.Peering().AddPeer(peercore.AddrInfo{ID: n1.ID(), Addrs: n1.Peer().Addrs()})
		}()
	}
	wg.Wait()
}

// OnClose hands the mesh something to close before its peers go — a service
// running on one of them.
func (m *Mesh) OnClose(c io.Closer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closers = append(m.closers, c)
}

// Close stops everything handed to OnClose, then the peers, then removes the
// mesh's directory.
//
// SERVICES FIRST, THEN PEERS, with a drain between: a service still publishing
// or answering when its host goes turns an orderly shutdown into a run of
// errors from a closed host.
func (m *Mesh) Close() {
	m.mu.Lock()
	closers, peers := m.closers, m.peers
	m.closers, m.peers = nil, nil
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, c := range closers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()

	m.cancel()
	time.Sleep(drain)

	for _, n := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Close()
		}()
	}
	wg.Wait()

	os.RemoveAll(m.root)
}
