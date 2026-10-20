// The certificate half of the globals service. See doc.go for the service,
// and the notes here for how a certificate is issued once for the cloud.
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
// a node joining, one falling out of the window, two regions split — so the
// order is also taken under a lease: the issuer writes /lease/<name>/<its id>
// and reads the prefix back, and of the live leases the earliest, then the
// lowest peer id, is the one that orders; having won, it reads the stored
// certificate once more before ordering, so a name another node has just
// renewed is not ordered again.
//
// THIS IS NOT A STRICT SINGLE ISSUER. kvdb has no conditional write and this
// service runs no consensus, so two nodes that cannot see each other's lease —
// a partition, or a write inside one replication delay — can both order. A
// duplicate order costs the CA one more certificate and nothing else: both
// are kept and the newest is read. Strict exclusion across regions is not
// provided here.
//
// # The challenges
//
// A name in the cloud's zone, and the zone's wildcard, are proved by DNS-01
// unless the operator says the zone is served elsewhere (Challenge). A DNS-01
// value is short-lived, so it is not a record: it is a pkg/cache entry with a
// lifetime of ChallengeTTL, over the challenge store the node gives
// (WithChallenges) — the same replicated cache a workload's cache resource is
// — so it expires on its own, and any node holding that store answers it.
// HTTP-01 is answered by any gateway from the account's thumbprint alone and
// stores nothing.
//
// The account key is shared: every globals node adopts the same one. A node
// finding none mints its own under its own name and then adopts the
// lowest-named key in the vault, so concurrent mints converge on one.
package globals

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

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	pkgcache "github.com/taubyte/tau/pkg/cache"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
)

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

// The challenge types a zone can be proved by.
const (
	ChallengeDNS  = "dns-01"
	ChallengeHTTP = "http-01"
)

var (
	ErrNoHost       = errors.New("globals: a service needs a host")
	ErrNoDB         = errors.New("globals: a service needs a database")
	ErrNoVault      = errors.New("globals: keys are kept in the secrets service, and this node reaches none")
	ErrNoChallenges = errors.New("globals: DNS-01 needs a store to hold its values in")
	ErrNoZone       = errors.New("globals: issuance needs the cloud's zone")
	ErrNotZone      = errors.New("globals: only names in the cloud's zone are issued")
	ErrNotKept      = errors.New("globals: nothing is kept for that name")
	ErrIssuing      = errors.New("globals: another node holds the lease on that name")
	ErrDisabled     = acme.ErrDisabled
)

// Config is what an operator writes under the node's `globals:`.
type Config struct {
	// Certificate is how the cloud issues its TLS certificates.
	Certificate CertificateConfig `yaml:"certificate"`
}

// CertificateConfig is the ACME CA and what is issued from it.
type CertificateConfig struct {
	// ACME is the CA issued from: Let's Encrypt unless a directory is named,
	// with an external account binding when the CA wants one.
	ACME pkgcertificate.ACME `yaml:"acme"`

	// Wildcard issues `*.<zone>` by DNS-01 and answers every one-label name
	// under the zone — the cloud's services — with it.
	Wildcard bool `yaml:"wildcard"`

	// Challenge is how a name in the zone is proved: ChallengeDNS, the
	// default, for a cloud whose dns nodes answer its zone, or ChallengeHTTP
	// for one whose zone is served elsewhere and points at its gateways. A
	// wildcard is DNS-01 or nothing.
	Challenge string `yaml:"challenge"`

	// Zone is the cloud's fqdn. The node fills it from the gateway's.
	Zone string `yaml:"-"`

	// RenewEvery is how often the renewal loop looks. Zero is
	// DefaultRenewEvery.
	RenewEvery time.Duration `yaml:"renewEvery"`

	// Lease bounds how long one node's claim on an order holds. Zero is
	// DefaultLease.
	Lease time.Duration `yaml:"lease"`
}

// ChallengeTTL is how long a DNS-01 value lives unless withdrawn first.
const ChallengeTTL = 10 * time.Minute

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

// WithChallenges is where DNS-01 values are held: a replicated store every
// dns node also reads (ChallengesIn). Required for DNS-01.
func WithChallenges(st pkgcache.Store) Option { return func(s *Service) { s.chStore = st } }

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
	cfg        CertificateConfig
	challenges *Challenges
	chStore    pkgcache.Store
	host       host.Host
	db         kvdb.KVDB
	vault      Vault
	peers      func() []peer.ID
	names      func() []string
	now        func() time.Time
	logf       func(string, ...any)

	issuerMu sync.Mutex
	issuer   *acme.Issuer

	server *rpc.Server
	stop   context.CancelFunc
	done   chan struct{}
}

// New builds a service. It reaches no CA and no vault until it issues.
func New(h host.Host, db kvdb.KVDB, whole Config, opts ...Option) (*Service, error) {
	cfg := whole.Certificate
	switch {
	case h == nil:
		return nil, ErrNoHost
	case db == nil:
		return nil, ErrNoDB
	}
	if err := acme.Check(cfg.ACME); err != nil {
		return nil, fmt.Errorf("globals: %w", err)
	}
	cfg.Zone = strings.ToLower(strings.Trim(cfg.Zone, "."))
	if cfg.Zone == "" {
		return nil, ErrNoZone
	}
	switch cfg.Challenge {
	case "":
		cfg.Challenge = ChallengeDNS
	case ChallengeDNS:
	case ChallengeHTTP:
		if cfg.Wildcard {
			return nil, errors.New("globals: a wildcard is proved by dns-01 only")
		}
	default:
		return nil, fmt.Errorf("globals: challenge %q is neither %s nor %s", cfg.Challenge, ChallengeDNS, ChallengeHTTP)
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
	if cfg.Challenge == ChallengeDNS {
		if s.chStore == nil {
			return nil, ErrNoChallenges
		}
		c, err := ChallengesIn(context.Background(), s.chStore, s.now)
		if err != nil {
			return nil, err
		}
		s.challenges = c
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
	opts := []acme.Option{
		acme.WithStore(store{s}),
		acme.WithAccountKey(key),
		acme.WithClock(s.now),
		acme.WithLogger(s.logf),
	}
	if s.cfg.Challenge == ChallengeDNS {
		opts = append(opts, acme.WithDNS(s.cfg.Zone, s.challenges))
	}
	issuer, err := acme.New(s.cfg.ACME, "", opts...)
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
			return nil, false, fmt.Errorf("globals: listing account keys: %w", err)
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
			return nil, false, fmt.Errorf("globals: reading the account key: %w", err)
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
	if err := s.vault.Set(ctx, VaultProject, VaultEnvironment, prefix+s.host.ID().String(), blob); err != nil {
		return nil, fmt.Errorf("globals: keeping the account key: %w", err)
	}
	key, ok, err := adopt()
	if err == nil && !ok {
		err = errors.New("globals: the account key just kept is not listed")
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
	// READ AGAIN HAVING WON: a node that held the lease a moment ago may have
	// stored a fresh one since this node's first look.
	if again, err := s.newest(ctx, name); err == nil && !s.due(again) {
		return again, nil
	}
	issuer, err := s.acmeIssuer(ctx)
	if err != nil {
		return have, err
	}
	if _, err := issuer.Issue(name); err != nil {
		if have != nil {
			s.logf("globals: renewing %s: %v", name, err)
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
			s.logf("globals: keeping %s issued: %v", name, err)
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
		s.logf("globals: listing kept certificates: %v", err)
	}
	for _, n := range kept {
		add(n)
	}
	return out
}
