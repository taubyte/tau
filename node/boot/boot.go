// Package boot turns a file an operator wrote into a running v2 node.
//
// # What this is, and what it deliberately is not
//
// It is the last mile: read a yaml file, build the machine's resources — a
// libp2p identity, a gossipsub, a place to keep data — hand them to
// node/, and serve what comes back.
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
// # Admission is the default and an open network is a decision
//
// Every v2 service trusts the peers it is connected to — that is the OSS trust
// model, stated in .notes/tee/v2-seam.md — so a peer is admitted only by a
// certificate from the cloud's CA, checked in the libp2p handshake in both
// directions (p2p/sectls). A node with no certificate would be a node whose
// register any stranger can publish into, so omitting one does not open the
// network; it refuses to start, and `open: true` is how somebody says they
// meant it.
package boot

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
	"github.com/taubyte/tau/pkg/assets"
	"github.com/taubyte/tau/pkg/kvdb"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/stats"
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

	// CA is the root certificate of the cloud's CA, PEM. Certificate is this
	// node's, PEM, issued by that CA for privatekey's public key and for
	// node.cloud. See the package doc: either empty is refused rather than
	// assumed, and so is a certificate this node's peers would refuse.
	CA          string `yaml:"ca"`
	Certificate string `yaml:"certificate"`

	// Open says an open network was meant. It exists so that the refusal above
	// is answerable, and so that answering it is a line somebody wrote.
	Open bool `yaml:"open"`

	// HTTP is where the gateway is served in the clear, e.g. ":8080". Empty
	// serves nothing there, which is right for a node that runs no gateway.
	HTTP string `yaml:"http"`

	// HTTPS is where the gateway is served over TLS, e.g. ":8443". Empty
	// terminates nothing, and a node facing the internet then sits behind
	// something that does.
	//
	// EACH HANDSHAKE IS ANSWERED BY THE GATEWAY THAT SERVES ITS NAME: the
	// cloud's own wildcard (node.gateway.wildcard) for a generated name, the
	// secret a gateway names for one that provides its own, and an ACME
	// issuance (node.cm.certificate) for a
	// generated name the wildcard does not cover — see services/gateway's
	// tls.go. The CA's HTTP-01 challenges are answered on HTTP.
	HTTPS string `yaml:"https"`
}

var logger = log.Logger("tau.v2.boot")

var (
	ErrNoCertificate = errors.New("boot: this node would join an open network; give it a ca and a certificate, or say open: true")
	ErrNoListen      = errors.New("boot: a node that binds nothing cannot be reached")
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
	if (f.CA == "" || f.Certificate == "") && !f.Open {
		return File{}, ErrNoCertificate
	}

	// AND THE KEY IS CHECKED WHERE THE FILE IS READ. A key that is not base64
	// is a typo in a file, and finding it after a libp2p host and a datastore
	// have been built reports it as a boot failure rather than as the line it
	// is. The certificate is checked in admission, against that key.
	if _, err := decode("privatekey", f.PrivateKey); err != nil {
		return File{}, err
	}
	return f, nil
}

// Running is a node and what it is being served on.
type Running struct {
	Node *node.Node

	// Found is what answered for the service Reach was asked for, captured at
	// the moment it answered.
	//
	// IT IS NOT RE-ASKED. A second Route can come back empty — the view has a
	// freshness window and the node that answered may have fallen out of it
	// between the two calls — and a caller that re-asked would be handed an
	// empty slice with no way to tell "gone" from "never was". Start leaves
	// this nil.
	Found []stats.Node

	peer   peer.Node
	server *http.Server

	// secure is the TLS listener, when the file names one.
	secure *http.Server

	// temporary is a data directory this process made and owns, removed on
	// Close. A node started from an operator's file has none.
	temporary string
}

// Start builds the machine's resources, starts the node, and serves its gateway
// when it has one. added are options a composer adds to the node, such as
// services added with node.WithService that f's service list may then name.
func Start(ctx context.Context, f File, added ...node.Option) (*Running, error) {
	f, err := f.withDefaults()
	if err != nil {
		return nil, err
	}

	privateKey, err := identity(f.PrivateKey)
	if err != nil {
		return nil, err
	}
	admission, err := f.admission(privateKey)
	if err != nil {
		return nil, err
	}

	bootstrap, err := addrInfos(f.Bootstrap)
	if err != nil {
		return nil, err
	}

	// A NODE WITH PEERS DIALS THEM; A NODE WITH NONE IS THE FIRST ONE. Those are
	// different constructors, and handing the second one an empty peer list is
	// not the same thing as the first: it bootstraps against nobody and then
	// waits for a swarm it will never be introduced to.
	var p peer.Node
	if len(bootstrap) > 0 {
		p, err = peer.NewWithBootstrapList(ctx, f.Root, privateKey, admission, f.Listen, f.Announce, !f.Open, bootstrap)
	} else {
		p, err = peer.New(ctx, f.Root, privateKey, admission, f.Listen, f.Announce, !f.Open, false)
	}
	if err != nil {
		return nil, fmt.Errorf("boot: libp2p: %w", err)
	}

	// The asset store is over the same peer, for the same reason raft is: the
	// content-addressed store IS the DAG, and only this layer has one.
	store, err := assets.New(p)
	if err != nil {
		p.Close()
		return nil, err
	}

	// WHAT THIS MACHINE CAN CONNECT TO, built before the node so that a build
	// with a broken engine refuses to start rather than serving every database
	// query with a message about a registry. See engines.go.
	dbEngines, err := DatabaseEngines(ctx)
	if err != nil {
		p.Close()
		return nil, err
	}

	// A NODE THAT TERMINATES SAYS SO before its gateway is built, because the
	// gateway decides from it what a name providing its own certificate gets
	// over plain HTTP.
	var serving []node.Option
	if f.HTTPS != "" {
		serving = append(serving, node.WithTLS())
	}

	n, err := node.New(f.Node, append(append(serving,
		node.WithHost(p.Peer()),
		node.WithDatabaseEngines(dbEngines),
		node.WithPubSub(p.Messaging()),
		node.WithDatabases(kvdb.New(p)),
		// RAFT IS OPENED HERE BECAUSE raft.New TAKES A peer.Node — a host, a
		// DHT, a datastore and a lifetime — and this is the layer that has one.
		// The node is handed a function so a composition that is not a machine
		// (dream) can hand it something else.
		node.WithRaft(func(namespace string) (raft.Cluster, error) {
			// SNAPSHOTS GO UNDER THIS NODE'S OWN ROOT. The default is a
			// machine-wide /tmp path keyed by namespace alone, so two nodes
			// that open the same group name share one snapshot store and
			// write each other's state — latent while there was one group
			// per node, and multiplied by a group per database.
			return raft.New(p, namespace, raft.WithSnapshotRoot(filepath.Join(f.Root, "raft")))
		}),
		node.WithAssets(store),
		// A VOLUME IS THE ONE THING A NODE KEEPS AS A DIRECTORY, so it gets the
		// root the rest of this file already has. Without it a container that
		// declares a mount is refused rather than started with an empty layer.
		node.WithRoot(f.Root),
	), added...)...)
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
	if err := r.serve(f.HTTP, f.HTTPS); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// addrInfos reads the config's bootstrap list.
//
// EVERY ADDRESS CARRIES A PEER ID. A bare /ip4/…/tcp/… names a socket and not
// who is on the other end of it, which on a private swarm is the whole
// question — so an address without /p2p/ is refused here rather than dropped
// silently into a node that then finds nobody.
func addrInfos(addresses []string) ([]libp2ppeer.AddrInfo, error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	out := make([]libp2ppeer.AddrInfo, 0, len(addresses))
	for _, address := range addresses {
		ma, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			return nil, fmt.Errorf("boot: bootstrap %q: %w", address, err)
		}
		info, err := libp2ppeer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			return nil, fmt.Errorf("boot: bootstrap %q: %w", address, err)
		}
		out = append(out, *info)
	}
	return out, nil
}

// serve binds the gateway's handler, when there is one and somewhere to put
// it: in the clear on one address, over TLS on the other, or both.
func (r *Running) serve(address, secure string) error {
	handler := r.Node.Handler()
	if handler == nil {
		return nil
	}

	var err error
	if address != "" {
		if r.server, err = listen(address, handler, nil); err != nil {
			return err
		}
	}
	if secure != "" {
		if r.secure, err = listen(secure, handler, r.Node.TLSConfig()); err != nil {
			return err
		}
	}
	return nil
}

// listen binds one address and serves the handler on it, over TLS when a
// config is given.
func listen(address string, handler http.Handler, config *tls.Config) (*http.Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("boot: listening on %s: %w", address, err)
	}

	// READ AND HEADER TIMEOUTS ARE NOT OPTIONAL ON A PUBLIC LISTENER. A
	// connection that never finishes its request line parks a goroutine until
	// the peer goes away, and the peer is the internet.
	//
	// Addr IS WHAT WAS TAKEN, not what was asked for. Serve never reads it —
	// only ListenAndServe does — so it is carried for Address(), and an
	// operator who wrote `:0` has no other way to learn the port. It was left
	// unset, which made Address() answer "" for every node that was serving.
	server := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         config,
	}

	// THE CERTIFICATE COMES FROM GetCertificate, per name, so no file is
	// passed: ServeTLS with empty paths uses the config as it is.
	if config != nil {
		go server.ServeTLS(listener, "", "")
		return server, nil
	}
	go server.Serve(listener)
	return server, nil
}

// Host is the libp2p host this node answers on, for a caller that started one
// to find the fleet and now has to dial what it found.
func (r *Running) Host() host.Host {
	if r.peer == nil {
		return nil
	}
	return r.peer.Peer()
}

// Address is where the gateway is being served in the clear, or "".
func (r *Running) Address() string {
	if r.server == nil {
		return ""
	}
	return r.server.Addr
}

// SecureAddress is where the gateway is being served over TLS, or "".
func (r *Running) SecureAddress() string {
	if r.secure == nil {
		return ""
	}
	return r.secure.Addr
}

// Close stops serving, then the node, then the machine's resources.
func (r *Running) Close() error {
	var err error
	if r.server != nil {
		err = r.server.Close()
	}
	if r.secure != nil {
		if closeErr := r.secure.Close(); err == nil {
			err = closeErr
		}
	}
	if r.Node != nil {
		if closeErr := r.Node.Close(); err == nil {
			err = closeErr
		}
	}
	if r.peer != nil {
		r.peer.Close()
	}
	if r.temporary != "" {
		os.RemoveAll(r.temporary)
	}
	return err
}

// identity is this node's libp2p key, minted when the file names none.
//
// A NEW IDENTITY EVERY RESTART IS ALMOST NEVER WHAT SOMEBODY MEANT. Anything
// that remembered this peer — a bootstrap list, another node's discovery view, a
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
// admission is nil for an open node with no certificate, which then speaks
// stock libp2p TLS.
func (f File) admission(privateKey []byte) (*sectls.Credentials, error) {
	if f.CA == "" && f.Certificate == "" {
		return nil, nil
	}
	key, err := crypto.UnmarshalPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("boot: privatekey: %w", err)
	}
	creds, err := sectls.NewCredentials([]byte(f.CA), []byte(f.Certificate), f.Node.Cloud, key)
	if err != nil {
		return nil, fmt.Errorf("boot: %w", err)
	}
	return creds, nil
}

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

// Reach starts the smallest node that can find the fleet: it runs discovery and
// nothing else, joins the swarm this file describes, and waits until it has
// seen a node running the service asked for.
//
// IT IS A NODE AND NOT A DIAL. Which node runs a service is a routing question
// answered from live measurements, and a command that dialled a peer an
// operator typed would be a second answer to it — one that is wrong whenever
// the fleet moved. This joins, listens, and asks the same view everything else
// asks.
func Reach(ctx context.Context, f File, service string, within time.Duration) (*Running, error) {
	f.Node = node.Config{Services: []string{node.Discovery}, Cloud: f.Node.Cloud, Discovery: f.Node.Discovery}
	f.HTTP = ""

	// A CLIENT DOES NOT KEEP THE FLEET'S DATA. Its own directory, removed with
	// it, so running one never touches what a node on this machine holds.
	root, err := os.MkdirTemp("", "tau-reach-")
	if err != nil {
		return nil, fmt.Errorf("boot: %w", err)
	}
	f.Root = root

	running, err := Start(ctx, f)
	if err != nil {
		os.RemoveAll(root)
		return nil, err
	}
	running.temporary = root

	deadline := time.After(within)
	for {
		if found := running.Node.Discovery().Route(service, f.Node.Discovery.Freshness()); len(found) > 0 {
			running.Found = found
			return running, nil
		}

		select {
		case <-ctx.Done():
			running.Close()
			return nil, ctx.Err()
		case <-deadline:
			running.Close()
			return nil, fmt.Errorf("boot: no %s answered within %s; is the fleet reachable and is this certificate from its CA", service, within)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// ErrNoIdentity is an init with no privatekey: a minted identity would write
// its key share for a peer that never starts.
var ErrNoIdentity = errors.New("boot: init needs the node's privatekey, the identity it will start with")

// Init writes a sealed bundle of secrets into this node's vault, on a node that
// is not running. It holds the node's identity and its data directory for as
// long as it runs, binds only loopback, and dials nobody.
func Init(ctx context.Context, f File, bundle *secretsv2.InitBundle) error {
	f, err := f.withDefaults()
	if err != nil {
		return err
	}
	if f.PrivateKey == "" {
		return ErrNoIdentity
	}

	privateKey, err := identity(f.PrivateKey)
	if err != nil {
		return err
	}
	admission, err := f.admission(privateKey)
	if err != nil {
		return err
	}

	p, err := peer.New(ctx, f.Root, privateKey, admission, []string{initListen}, nil, !f.Open, false)
	if err != nil {
		return fmt.Errorf("boot: libp2p: %w", err)
	}
	defer p.Close()

	return node.InitSecrets(ctx, f.Node, f.Root, p.Peer(), kvdb.New(p), bundle)
}

// initListen keeps init off the ports the node itself binds.
const initListen = "/ip4/127.0.0.1/tcp/0"
