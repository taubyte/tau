// Package boot turns a file an operator wrote into a running v2 node: read yaml,
// build the machine's resources (a libp2p identity, gossipsub, a data directory),
// hand them to node/, and serve what comes back.
//
// The file is node.Config plus what a machine has rather than a cloud (listen
// addresses, bootstrap peers, data directory): a decode, not a translation. v1's
// shape files are not read.
//
// Every v2 service trusts its connected peers, so a peer is admitted only by a
// certificate from the cloud's CA, checked both ways in the libp2p handshake
// (p2p/sectls). A node without one refuses to start; `open: true` says an open
// network was meant.
package boot

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/secrets"
)

// File is what an operator writes.
type File struct {
	// Node is every v2 service's own configuration, unchanged.
	Node instance.Config `yaml:"node"`

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

	// HTTPS is where the gateway is served over TLS, e.g. ":8443"; empty terminates
	// nothing. Each handshake is answered by the gateway serving its name: the cloud's
	// wildcard (node.gateway.wildcard), a gateway's own secret, or an ACME issuance
	// (node.cm.certificate) for a generated name the wildcard misses (services/gateway's
	// tls.go). HTTP-01 challenges are answered on HTTP.
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

// Running is an instance and what it is being served on.
type Running struct {
	*instance.Instance

	server *http.Server

	// secure is the TLS listener, when the file names one.
	secure *http.Server
}

// Start builds the machine's resources, starts the node, and serves its gateway
// when it has one. added are options a composer adds to the node, such as
// services added with node.WithService that f's service list may then name.
func Start(ctx context.Context, f File, added ...instance.Option) (*Running, error) {
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

	// The instance's directory is the file's, so its raft snapshots, volumes and
	// secrets share live on this machine's disk.
	cfg := f.Node
	cfg.Root = f.Root

	// A NODE THAT TERMINATES TELLS ITS GATEWAY, in the gateway's own section, since
	// the gateway decides from it what a name providing its own certificate gets over
	// plain HTTP.
	if f.HTTPS != "" {
		if cfg, err = servedOverTLS(cfg); err != nil {
			p.Close()
			return nil, err
		}
	}

	n, err := instance.New(p, cfg, added...)
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := n.Start(ctx); err != nil {
		n.Close()
		p.Close()
		return nil, err
	}

	r := &Running{Instance: n}
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
	handler := r.Handler()
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
		if r.secure, err = listen(secure, handler, r.TLSConfig()); err != nil {
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

	// Read and header timeouts are mandatory on a public listener. Addr is what was
	// bound, carried for Address() so an operator who wrote `:0` learns the port.
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
	// The instance first, then the peer it was made from, which this process
	// started and owns.
	if r.Instance != nil {
		if closeErr := r.Instance.Close(); err == nil {
			err = closeErr
		}
		r.Instance.Node.Close()
	}
	return err
}

// identity is this node's libp2p key, minted and logged when the file names none,
// since a new identity every restart makes the node a stranger to its fleet.
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

// admission is nil for an open node with no certificate, which then speaks stock
// libp2p TLS.
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

// servedOverTLS is cfg with its gateway's section saying it is served over TLS too,
// beside whatever else the section says.
func servedOverTLS(cfg instance.Config) (instance.Config, error) {
	var section gateway.Config
	if err := cfg.Section(instance.Gateway, &section); err != nil {
		return cfg, err
	}
	section.TLS = true
	sections := maps.Clone(cfg.Sections)
	if sections == nil {
		sections = map[string]any{}
	}
	sections[instance.Gateway] = section
	cfg.Sections = sections
	return cfg, nil
}

// sectionOf is cfg's section for one service alone, or none.
func sectionOf(cfg instance.Config, name string) map[string]any {
	section, ok := cfg.Sections[name]
	if !ok {
		return nil
	}
	return map[string]any{name: section}
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

	if len(f.Node.Services) > 0 && !slices.Contains(f.Node.Services, secrets.Descriptor.Name) {
		return ErrNoSecretsHere
	}
	var section secrets.Config
	if err := f.Node.Section(secrets.Descriptor.Name, &section); err != nil {
		return err
	}
	return secrets.Init(ctx, p, f.Root, section, bundle)
}

// ErrNoSecretsHere is an init of a node whose file runs no secrets service.
var ErrNoSecretsHere = errors.New("boot: this node runs no secrets service to initialize")

// initListen keeps init off the ports the node itself binds.
const initListen = "/ip4/127.0.0.1/tcp/0"
