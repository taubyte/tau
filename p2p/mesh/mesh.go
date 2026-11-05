// Package mesh runs a set of libp2p peers inside one process, on loopback, meshed to
// each other: the network a v2 end-to-end test stands its services on, without the
// v1 service registry dream carries.
//
// It keeps what was measured in dream's network: one dial per pair from a canonical
// side with nothing else dialing (see dial and Add), and the bounded WaitForSwarm
// after it. A peer's services start before it joins. dream's 750ms settle is not
// here: it was only ever for a simple, and a mesh has none.
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

	"github.com/libp2p/go-libp2p/core/crypto"
	peercore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
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

	// A universe can run for as long as somebody leaves it up.
	certificateValidity = 10 * 365 * 24 * time.Hour

	// drain is how long Close lets services finish before their peers go.
	drain = 300 * time.Millisecond
)

// Mesh is a set of meshed peers and whatever runs on them.
type Mesh struct {
	ctx    context.Context
	cancel context.CancelFunc

	name string
	root string
	ca   *sectls.CA

	mu      sync.RWMutex
	peers   []peer.Node
	guests  []peer.Node
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

	// A CA per mesh, so a peer of one mesh cannot join another on the same loopback
	// ports.
	ca, err := sectls.NewCA(name, certificateValidity)
	if err != nil {
		os.RemoveAll(root)
		return nil, err
	}

	m := &Mesh{name: name, root: root, ca: ca}
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

// Add makes a peer, starts what runs on it, and then dials it to every peer already
// here. Starting first means a service's first moves do not race the connection.
// start may be nil.
func (m *Mesh) Add(start func(peer.Node) error) (peer.Node, error) {
	n, err := m.join(start)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.peers = append(m.peers, n)
	m.mu.Unlock()
	return n, nil
}

// Guest is a peer meshed with every one here that runs nothing and is not one of
// Peers: how the mesh is reached from outside what runs on it, such as by whoever
// made it. It closes with the mesh.
func (m *Mesh) Guest() (peer.Node, error) {
	n, err := m.join(nil)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.guests = append(m.guests, n)
	m.mu.Unlock()
	return n, nil
}

// join makes a peer, starts what runs on it, and dials it to every peer already here.
func (m *Mesh) join(start func(peer.Node) error) (peer.Node, error) {
	existing := m.Peers()

	m.mu.Lock()
	dir := filepath.Join(m.root, fmt.Sprintf("peer-%d", m.added))
	m.added++
	m.mu.Unlock()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}

	// No bootstrap list: the dial below is the one way a peer joins. A background
	// bootstrap dial crosses it, both handshakes park in dial backoff, and a CRDT on the
	// pair never fetches its heads (bootstrap plus dial resolved a publish one run in
	// three; the dial alone, every run).
	key := keypair.NewRaw()
	admission, err := m.Credentials(key)
	if err != nil {
		return nil, err
	}
	n, err := peer.NewPublic(m.ctx, dir, key, admission, []string{listen}, []string{listen}, peer.StandAlone())
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
	return n, nil
}

// dial connects the new peer to every existing one, once per pair from a canonical
// side: crossed dials make both TLS handshakes the client and park both sides in dial
// backoff for tens of seconds. A failed dial is retried with fresh addresses before
// falling back to the peering service, whose backoff can leave a mesh half-meshed.
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
				info := peercore.AddrInfo{ID: n1.ID(), Addrs: n1.Host().Addrs()}
				if len(info.Addrs) > 0 {
					if err := n0.Host().Connect(ctx, info); err == nil {
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
			n0.Peering().AddPeer(peercore.AddrInfo{ID: n1.ID(), Addrs: n1.Host().Addrs()})
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

// Close stops everything handed to OnClose, then the peers, then removes the mesh's
// directory. Services go first, with a drain, so none is still talking to a closed
// host.
func (m *Mesh) Close() {
	m.mu.Lock()
	closers, peers := m.closers, append(m.peers, m.guests...)
	m.closers, m.peers, m.guests = nil, nil, nil
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

// CA is the mesh's certificate authority.
func (m *Mesh) CA() *sectls.CA { return m.ca }

// Credentials issues a libp2p key, marshalled, its certificate for this mesh,
// and checks it the way a booting node checks its own.
func (m *Mesh) Credentials(key []byte) (*sectls.Credentials, error) {
	private, err := crypto.UnmarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	cert, err := m.ca.Issue(private.GetPublic(), m.name, certificateValidity)
	if err != nil {
		return nil, err
	}
	return sectls.NewCredentials(m.ca.CertificatePEM(), cert, m.name, private)
}
