// Package boot turns a file an operator wrote into a running v2 node.
//
// # What this is, and what it deliberately is not
//
// It is the last mile: read a yaml file, build the machine's resources — a
// libp2p identity, a gossipsub, a place to keep data — hand them to
// services/v2/node, and serve what comes back.
//
// IT IS NOT A SECOND CONFIGURATION SYSTEM. The file's shape IS node.Config plus
// the handful of things a machine has rather than a cloud: where it listens,
// who it dials, where its data lives. Every v2 service's Config was written to
// be a plain value that survives a round trip through yaml precisely so that
// this layer is a decode and not a translation — a translation is where an
// operator's setting quietly stops meaning what the struct says.
//
// It does not read v1's shape files. Those carry v1's concepts, and a v2 node
// that consumed them would have to decide what each of them means here, which
// is a mapping nobody could check.
//
// # A private network is the default and an open one is a decision
//
// A libp2p host with no pre-shared key joins the public network. Every v2
// service trusts its swarm — that is the OSS trust model, stated in
// .notes/tee/v2-seam.md — so a node with no key is a node whose register any
// stranger can publish into. Omitting the key therefore does not open the
// network; it refuses to start, and `open: true` is how somebody says they
// meant it.
package boot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/crypto"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/v2/node"
)

// File is what an operator writes.
type File struct {
	// Node is every v2 service's own configuration, unchanged.
	Node node.Config `yaml:"node"`

	// Root is where this node keeps its data.
	Root string `yaml:"root"`

	// Listen and Announce are the addresses this node binds and advertises.
	// Announce empty means announce what was bound.
	Listen   []string `yaml:"listen"`
	Announce []string `yaml:"announce"`

	// Bootstrap are peers to dial on start. A node with none finds nobody and
	// is a fleet of one, which is a legitimate development shape and a
	// surprising production one.
	Bootstrap []string `yaml:"bootstrap"`

	// PrivateKey is this node's libp2p identity, base64. Empty mints one and
	// says so: a new identity every restart means anything that remembered
	// this peer forgets it.
	PrivateKey string `yaml:"privatekey"`

	// SwarmKey is the pre-shared key that makes this a private network,
	// base64. See the package doc: empty is refused rather than assumed.
	SwarmKey string `yaml:"swarmkey"`

	// Open says an open network was meant. It exists so that the refusal above
	// is answerable, and so that answering it is a line somebody wrote.
	Open bool `yaml:"open"`

	// HTTP is where the gateway is served, e.g. ":8080". Empty serves nothing,
	// which is right for a node that runs no gateway.
	//
	// THERE IS NO TLS HERE. Where a certificate comes from is the auth
	// service's question and a gateway that owned an ACME client would own it
	// twice; a node facing the internet sits behind something that terminates,
	// or serves this handler from a caller that does.
	HTTP string `yaml:"http"`
}

var logger = log.Logger("tau.v2.boot")

var (
	ErrNoSwarmKey = errors.New("boot: this node would join the public network; give it a swarmkey, or say open: true")
	ErrNoListen   = errors.New("boot: a node that binds nothing cannot be reached")
)

// Defaults.
const (
	DefaultRoot   = "/tb/node"
	DefaultListen = "/ip4/0.0.0.0/tcp/4242"
)

// Read parses a file and applies defaults.
func Read(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("boot: reading %s: %w", path, err)
	}

	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("boot: %s: %w", path, err)
	}
	return f.withDefaults()
}

func (f File) withDefaults() (File, error) {
	if f.Root == "" {
		f.Root = DefaultRoot
	}
	if len(f.Listen) == 0 {
		f.Listen = []string{DefaultListen}
	}

	// THE REFUSAL IS HERE AND NOT AT THE HOST. libp2p will happily build a
	// host with no key, and by the time anything notices, the register is
	// answering strangers.
	if f.SwarmKey == "" && !f.Open {
		return File{}, ErrNoSwarmKey
	}
	return f, nil
}

// Running is a node and what it is being served on.
type Running struct {
	Node *node.Node

	peer   peer.Node
	server *http.Server
}

// Start builds the machine's resources, starts the node, and serves its gateway
// when it has one.
func Start(ctx context.Context, f File) (*Running, error) {
	f, err := f.withDefaults()
	if err != nil {
		return nil, err
	}

	privateKey, err := identity(f.PrivateKey)
	if err != nil {
		return nil, err
	}
	swarmKey, err := decode("swarmkey", f.SwarmKey)
	if err != nil {
		return nil, err
	}

	p, err := peer.New(ctx, f.Root, privateKey, swarmKey, f.Listen, f.Announce, !f.Open, len(f.Bootstrap) == 0)
	if err != nil {
		return nil, fmt.Errorf("boot: libp2p: %w", err)
	}

	n, err := node.New(f.Node,
		node.WithHost(p.Peer()),
		node.WithPubSub(p.Messaging()),
		node.WithDatabases(kvdb.New(p)),
	)
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := n.Start(ctx); err != nil {
		n.Close()
		p.Close()
		return nil, err
	}

	r := &Running{Node: n, peer: p}
	if err := r.serve(f.HTTP); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// serve binds the gateway's handler, when there is one and somewhere to put it.
func (r *Running) serve(address string) error {
	handler := r.Node.Handler()
	if handler == nil || address == "" {
		return nil
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("boot: listening on %s: %w", address, err)
	}

	// READ AND HEADER TIMEOUTS ARE NOT OPTIONAL ON A PUBLIC LISTENER. A
	// connection that never finishes its request line parks a goroutine until
	// the peer goes away, and the peer is the internet.
	r.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go r.server.Serve(listener)
	return nil
}

// Address is where the gateway is being served, or "".
func (r *Running) Address() string {
	if r.server == nil {
		return ""
	}
	return r.server.Addr
}

// Close stops serving, then the node, then the machine's resources.
func (r *Running) Close() error {
	var err error
	if r.server != nil {
		err = r.server.Close()
	}
	if r.Node != nil {
		if closeErr := r.Node.Close(); err == nil {
			err = closeErr
		}
	}
	if r.peer != nil {
		r.peer.Close()
	}
	return err
}

// identity is this node's libp2p key, minted when the file names none.
//
// A NEW IDENTITY EVERY RESTART IS ALMOST NEVER WHAT SOMEBODY MEANT. Anything
// that remembered this peer — a bootstrap list, another node's stats view, a
// pinned address — forgets it, and the node rejoins as a stranger. It is minted
// rather than refused because a first run has to be possible without generating
// a key by hand, and it is logged so the second run is a choice.
func identity(encoded string) ([]byte, error) {
	if encoded != "" {
		return decode("privatekey", encoded)
	}

	private, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, fmt.Errorf("boot: minting an identity: %w", err)
	}

	out, err := crypto.MarshalPrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("boot: minting an identity: %w", err)
	}

	logger.Warn("no privatekey: this node minted a new identity and will be a different peer after a restart; " +
		"put " + base64.StdEncoding.EncodeToString(out) + " in the config to keep it")
	return out, nil
}

// decode reads a base64 field, and says which one when it cannot.
func decode(name, value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	out, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("boot: %s is not base64: %w", name, err)
	}
	return out, nil
}
