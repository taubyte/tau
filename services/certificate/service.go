// Package certificate issues and keeps the certificates a cloud serves TLS
// with, for every node that serves it.
//
// # Why it is its own service
//
// The dns service owns the zone and the gateway owns port 80, and each is the
// answering half of one challenge type; neither is where an order should live.
// The dns service holds no state and binds :53 on a few nodes, and the gateway
// runs on every edge node, so issuing there was one issuance per node per name.
// Here one node orders a name, the result is kept where every certificate node
// reads it, and a node serving TLS asks for it.
//
// # What is kept, and where
//
// A certificate's chain is public and lives in this service's own database,
// one key per issuance (/cert/<name>/<not after, 20 digits>/<issuance id>), so
// two issuances never overwrite each other and the newest is the last key in
// byte order. Its private key and the ACME account key are secrets and live in
// the secrets service, under VaultProject; a node serving the certificate
// opens the key from its own vault.
//
// # One issuer per name, and what that rests on
//
// The node that orders a name is chosen by rendezvous hashing over the nodes
// reporting this service, so every node with the same view picks the same one
// and a renewal loop on each touches only its own names. Views can disagree —
// a node joining, one falling out of the window — so the order is also taken
// under a lease: the issuer writes /lease/<name>/<its id> and reads the
// prefix back, and of the live leases the earliest, then the lowest peer id,
// is the one that orders. kvdb has no conditional write, so this narrows
// duplicates to two nodes that wrote within one replication delay of each
// other; it cannot rule them out. A duplicate order costs the CA one more
// certificate and nothing else, because both are kept and the newest is read.
//
// # The challenges
//
// A name in the cloud's zone, and the zone's wildcard, are proved by DNS-01:
// the TXT value is written to /challenge/<fqdn>/<digest> and a dns node asks
// every certificate node for it, so the node that wrote it always answers
// from its own store with no replication to wait for. Anything else is proved
// by HTTP-01, which any gateway answers from the account's thumbprint alone.
//
// The account key is shared: every certificate node adopts the same one. A
// node finding none mints its own under its own name and then adopts the
// lowest-named key in the vault, so concurrent mints converge on one.
package certificate

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/pkg/proto/gen/tau/certificate/v2/certificatev2connect"
)

// Name is the service's name, in a node's service list and in discovery.
const Name = "certificate"

// Protocol is the libp2p protocol the service answers on.
var Protocol protocol.ID = rpc.Protocol(certificatev2connect.CertificateServiceName)

// VaultProject and VaultEnvironment scope the keys this service keeps in the
// secrets service. A project name cannot start with "@", so no project's
// secret is ever one of these.
const (
	VaultProject     = "@cloud"
	VaultEnvironment = "certificate"
)

// Defaults.
const (
	DefaultRenewEvery = time.Hour
	DefaultLease      = 5 * time.Minute
)

var (
	ErrNoHost   = errors.New("certificate: a service needs a host")
	ErrNoDB     = errors.New("certificate: a service needs a database")
	ErrNoVault  = errors.New("certificate: keys are kept in the secrets service, and this node reaches none")
	ErrNoZone   = errors.New("certificate: issuance needs the cloud's zone")
	ErrNotZone  = errors.New("certificate: only names in the cloud's zone are issued")
	ErrNotKept  = errors.New("certificate: nothing is kept for that name")
	ErrIssuing  = errors.New("certificate: another node holds the lease on that name")
	ErrDisabled = acme.ErrDisabled
)

// Config is what an operator writes under the node's `certificate:`.
type Config struct {
	// ACME is the CA issued from: Let's Encrypt unless a directory is named,
	// with an external account binding when the CA wants one.
	ACME pkgcertificate.ACME `yaml:"acme"`

	// Wildcard issues `*.<zone>` by DNS-01 and answers every one-label name
	// under the zone — the cloud's services — with it.
	Wildcard bool `yaml:"wildcard"`

	// Zone is the cloud's fqdn. The node fills it from the gateway's.
	Zone string `yaml:"-"`

	// RenewEvery is how often the renewal loop looks. Zero is
	// DefaultRenewEvery.
	RenewEvery time.Duration `yaml:"renewEvery"`

	// Lease bounds how long one node's claim on an order holds. Zero is
	// DefaultLease.
	Lease time.Duration `yaml:"lease"`
}

// Vault is the slice of the secrets service the keys are kept in.
type Vault interface {
	Get(ctx context.Context, project, environment, name string) ([]byte, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// Option configures a Service.
type Option func(*Service)

// WithVault is where keys are kept. Required to issue.
func WithVault(v Vault) Option { return func(s *Service) { s.vault = v } }

// WithPeers is the nodes reporting this service, asked at call time.
func WithPeers(f func() []peer.ID) Option { return func(s *Service) { s.peers = f } }

// WithNames is the names the renewal loop keeps issued beside those already
// kept, asked at each pass: the cloud's services by name.
func WithNames(f func() []string) Option { return func(s *Service) { s.names = f } }

// WithClock injects the clock. Tests use it.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithLogger is where what no caller waits on is said.
func WithLogger(logf func(string, ...any)) Option { return func(s *Service) { s.logf = logf } }

// Service issues, keeps and renews certificates.
type Service struct {
	cfg   Config
	host  host.Host
	db    kvdb.KVDB
	vault Vault
	peers func() []peer.ID
	names func() []string
	now   func() time.Time
	logf  func(string, ...any)

	issuerMu sync.Mutex
	issuer   *acme.Issuer

	server *rpc.Server
	stop   context.CancelFunc
	done   chan struct{}
}

// New builds a service. It reaches no CA and no vault until it issues.
func New(h host.Host, db kvdb.KVDB, cfg Config, opts ...Option) (*Service, error) {
	switch {
	case h == nil:
		return nil, ErrNoHost
	case db == nil:
		return nil, ErrNoDB
	case !cfg.ACME.Enabled:
		return nil, ErrDisabled
	}
	cfg.Zone = strings.ToLower(strings.Trim(cfg.Zone, "."))
	if cfg.Zone == "" {
		return nil, ErrNoZone
	}
	if cfg.RenewEvery <= 0 {
		cfg.RenewEvery = DefaultRenewEvery
	}
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultLease
	}
	s := &Service{
		cfg: cfg, host: h, db: db,
		peers: func() []peer.ID { return nil },
		names: func() []string { return nil },
		now:   time.Now,
		logf:  func(string, ...any) {},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Start answers on Protocol and starts the renewal loop.
func (s *Service) Start(ctx context.Context) error {
	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount())
	if err != nil {
		return err
	}
	s.server = server
	loop, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop, s.done = stop, make(chan struct{})
	go s.renewing(loop)
	return nil
}

// Close stops the loop and the listener.
func (s *Service) Close() error {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// Name normalises a name the way every key and order spells it.
func normal(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// inZone is a name this service issues: the zone, a name under it, or its
// wildcard.
func (s *Service) inZone(name string) bool {
	bare := strings.TrimPrefix(name, "*.")
	return bare == s.cfg.Zone || strings.HasSuffix(bare, "."+s.cfg.Zone)
}

// served is the certificate that answers name: the wildcard, when there is
// one and name is one label under the zone, and name's own otherwise.
func (s *Service) served(name string) string {
	if s.cfg.Wildcard {
		if label, ok := strings.CutSuffix(name, "."+s.cfg.Zone); ok && label != "" && !strings.Contains(label, ".") {
			return "*." + s.cfg.Zone
		}
	}
	return name
}

// Owner is the node rendezvous hashing picks to order name among peers.
func Owner(name string, peers []peer.ID) (peer.ID, bool) {
	var best peer.ID
	var bestScore string
	for _, p := range peers {
		sum := sha256.Sum256([]byte(string(p) + "\x00" + name))
		score := hex.EncodeToString(sum[:])
		if best == "" || score > bestScore || (score == bestScore && p < best) {
			best, bestScore = p, score
		}
	}
	return best, best != ""
}

// members is the peers reporting this service, with this node.
func (s *Service) members() []peer.ID {
	out := []peer.ID{s.host.ID()}
	for _, p := range s.peers() {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) owns(name string) bool {
	owner, _ := Owner(name, s.members())
	return owner == s.host.ID()
}

// acmeIssuer is the issuer, built on first use: it needs the account key,
// which is in the vault, which may not answer at construction.
func (s *Service) acmeIssuer(ctx context.Context) (*acme.Issuer, error) {
	s.issuerMu.Lock()
	defer s.issuerMu.Unlock()
	if s.issuer != nil {
		return s.issuer, nil
	}
	key, err := s.accountKey(ctx)
	if err != nil {
		return nil, err
	}
	issuer, err := acme.New(s.cfg.ACME, "",
		acme.WithStore(store{s}),
		acme.WithAccountKey(key),
		acme.WithDNS(s.cfg.Zone, presenter{s}),
		acme.WithClock(s.now),
		acme.WithLogger(s.logf),
	)
	if err != nil {
		return nil, err
	}
	s.issuer = issuer
	return issuer, nil
}

// accountKey is the operator's, or the one every certificate node adopts.
func (s *Service) accountKey(ctx context.Context) (crypto.Signer, error) {
	if s.cfg.ACME.AccountKey != "" {
		return acme.ParseKey([]byte(s.cfg.ACME.AccountKey))
	}
	if s.vault == nil {
		return nil, ErrNoVault
	}
	prefix := "account-" + digest(directoryOf(s.cfg.ACME)) + "-"
	adopt := func() (crypto.Signer, bool, error) {
		names, err := s.vault.List(ctx, VaultProject, VaultEnvironment)
		if err != nil {
			return nil, false, fmt.Errorf("certificate: listing account keys: %w", err)
		}
		var keys []string
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				keys = append(keys, n)
			}
		}
		if len(keys) == 0 {
			return nil, false, nil
		}
		slices.Sort(keys)
		blob, err := s.vault.Get(ctx, VaultProject, VaultEnvironment, keys[0])
		if err != nil {
			return nil, false, fmt.Errorf("certificate: reading the account key: %w", err)
		}
		key, err := acme.ParseKey(blob)
		return key, err == nil, err
	}
	if key, ok, err := adopt(); err != nil || ok {
		return key, err
	}
	_, blob, err := acme.MintKey()
	if err != nil {
		return nil, err
	}
	if err := s.vault.Set(ctx, VaultProject, VaultEnvironment, prefix+string(s.host.ID()), blob); err != nil {
		return nil, fmt.Errorf("certificate: keeping the account key: %w", err)
	}
	key, ok, err := adopt()
	if err == nil && !ok {
		err = errors.New("certificate: the account key just kept is not listed")
	}
	return key, err
}

func directoryOf(cfg pkgcertificate.ACME) string {
	if cfg.Directory == "" {
		return pkgcertificate.LetsEncrypt
	}
	return cfg.Directory
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// due is a certificate with less than a third of its lifetime left.
func (s *Service) due(k *kept) bool {
	lifetime := k.leaf.NotAfter.Sub(k.leaf.NotBefore)
	return k.leaf.NotAfter.Sub(s.now()) < lifetime/3
}

// Ensure is the newest certificate kept for name, issued now when there is
// none or it is due and this node wins the lease. A due certificate another
// node is renewing is returned as it is.
func (s *Service) Ensure(ctx context.Context, name string) (*kept, error) {
	name = s.served(normal(name))
	if !s.inZone(name) {
		return nil, fmt.Errorf("%w: %s is not in %s", ErrNotZone, name, s.cfg.Zone)
	}
	have, err := s.newest(ctx, name)
	if err != nil && !errors.Is(err, ErrNotKept) {
		return nil, err
	}
	if have != nil && !s.due(have) {
		return have, nil
	}
	won, err := s.claim(ctx, name)
	if err != nil {
		return have, err
	}
	if !won {
		if have != nil {
			return have, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrIssuing, name)
	}
	defer s.release(name)
	issuer, err := s.acmeIssuer(ctx)
	if err != nil {
		return have, err
	}
	if _, err := issuer.Issue(name); err != nil {
		if have != nil {
			s.logf("certificate: renewing %s: %v", name, err)
			return have, nil
		}
		return nil, err
	}
	return s.newest(ctx, name)
}

// renewing keeps every owned name issued.
func (s *Service) renewing(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.cfg.RenewEvery)
	defer ticker.Stop()
	for {
		s.renewPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// renewPass looks at every wanted or kept name once.
func (s *Service) renewPass(ctx context.Context) {
	for _, name := range s.wanted(ctx) {
		if ctx.Err() != nil {
			return
		}
		if !s.owns(name) {
			continue
		}
		if _, err := s.Ensure(ctx, name); err != nil && !errors.Is(err, ErrIssuing) {
			s.logf("certificate: keeping %s issued: %v", name, err)
		}
	}
}

// wanted is the wildcard, the names the node asks for, and every name kept.
func (s *Service) wanted(ctx context.Context) []string {
	var out []string
	add := func(n string) {
		n = s.served(normal(n))
		if n != "" && s.inZone(n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if s.cfg.Wildcard {
		add("*." + s.cfg.Zone)
	}
	for _, n := range s.names() {
		add(n)
	}
	kept, err := s.keptNames(ctx)
	if err != nil {
		s.logf("certificate: listing kept certificates: %v", err)
	}
	for _, n := range kept {
		add(n)
	}
	return out
}
