// The certificate half of the cm service.
//
// # What is kept, and where
//
// A certificate's chain is public and lives in this service's own database,
// one key per issuance (/cert/<name>/<not after, 20 digits>/<issuance id>), so
// two issuances never overwrite each other and the newest is the last key in
// byte order. Its private key and the ACME account key are secrets and live in
// the secrets service, in the cloud scope VaultService.
//
// # Who orders
//
// Any node that needs a certificate, as v1's autocert did. Before ordering it
// reads the store again and waits up to CertWait for one ordered elsewhere.
// Two nodes can still order the same name; both issuances are kept and the
// newest is read, so they converge.
//
// # The account
//
// One account for the whole cloud: the key in the vault under AccountSecret,
// read by every node. Without one nothing is issued. HTTP-01 therefore stores
// nothing: any gateway answers any token from the account's thumbprint.
//
// # DNS-01
//
// A DNS-01 value is a pkg/cache entry with a lifetime of ChallengeTTL, one per
// presentation, over the replicated challenge store every cm, dns and
// ordering node holds a copy of (Challenges).
package cm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
)

// VaultService is the cloud scope this service's keys are kept in. It is a
// scope no project can name.
const (
	VaultService = "cm"

	// AccountSecret is the vault name of the cloud's ACME account key, PEM.
	AccountSecret = "acme-account"
)

// The challenge types a zone can be proved by.
const (
	ChallengeDNS  = "dns-01"
	ChallengeHTTP = "http-01"
)

// ChallengeTTL is how long a DNS-01 value lives unless withdrawn first.
const ChallengeTTL = 10 * time.Minute

var (
	ErrNoHost       = errors.New("cm: a service needs a host")
	ErrNoDB         = errors.New("cm: a service needs a database")
	ErrNoVault      = errors.New("cm: keys are kept in the secrets service, and this node reaches none")
	ErrNoChallenges = errors.New("cm: DNS-01 needs a store to hold its values in")
	ErrNoZone       = errors.New("cm: issuance needs the cloud's zone")
	ErrNotZone      = errors.New("cm: only names in the cloud's zone are issued")
	ErrNotKept      = errors.New("cm: nothing is kept for that name")
	ErrNoAccount    = errors.New("cm: issuance needs the cloud's ACME account key, in the vault as cloud/" + VaultService + "/" + AccountSecret)
	ErrDisabled     = acme.ErrDisabled
)

// Config is what an operator writes under the node's `cm:`.
type Config struct {
	// Certificate is how the cloud issues its TLS certificates.
	Certificate CertificateConfig `yaml:"certificate"`

	// Resolver is the DNS server, as host:port, a custom domain's proof is looked
	// up at. Empty is the machine's own resolver.
	Resolver string `yaml:"resolver,omitempty"`
}

// resolver is what a custom domain's TXT proof is looked up through.
func (c Config) resolver() (Resolver, error) {
	if c.Resolver == "" {
		return net.DefaultResolver, nil
	}
	if _, _, err := net.SplitHostPort(c.Resolver); err != nil {
		return nil, fmt.Errorf("cm: resolver %q is not a host:port: %w", c.Resolver, err)
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, c.Resolver)
	}}, nil
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

	// Zone is the cloud's fqdn: the node's cloudfqdn, filled by whoever builds a client.
	Zone string `yaml:"-"`
}

// normalised checks c and fills its defaults.
func (c CertificateConfig) normalised() (CertificateConfig, error) {
	if err := acme.Check(c.ACME); err != nil {
		return c, fmt.Errorf("cm: %w", err)
	}
	c.Zone = strings.ToLower(strings.Trim(c.Zone, "."))
	if c.Zone == "" {
		return c, ErrNoZone
	}
	switch c.Challenge {
	case "":
		c.Challenge = ChallengeDNS
	case ChallengeDNS:
	case ChallengeHTTP:
		if c.Wildcard {
			return c, errors.New("cm: a wildcard is proved by dns-01 only")
		}
	default:
		return c, fmt.Errorf("cm: challenge %q is neither %s nor %s", c.Challenge, ChallengeDNS, ChallengeHTTP)
	}
	return c, nil
}

// normal spells a name the way every key and order does.
func normal(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// inZone is the zone, a name under it, or its wildcard.
func (c CertificateConfig) inZone(name string) bool {
	bare := strings.TrimPrefix(name, "*.")
	return bare == c.Zone || strings.HasSuffix(bare, "."+c.Zone)
}

// served is the certificate that answers name: the wildcard, when there is
// one and name is one label under the zone, and name's own otherwise.
func (c CertificateConfig) served(name string) string {
	if c.Wildcard {
		if label, ok := strings.CutSuffix(name, "."+c.Zone); ok && label != "" && !strings.Contains(label, ".") {
			return "*." + c.Zone
		}
	}
	return name
}

// Option configures a Store.
type Option func(*Store)

// WithClock injects the clock. Tests use it.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Store hosts the certificate store.
type Store struct {
	cfg    CertificateConfig
	host   host.Host
	db     kvdb.Instance
	now    func() time.Time
	server *rpc.Server

	vault    Vault
	resolver Resolver
	signer   signer
}

// Open is the certificate store over db, unstarted.
func Open(h host.Host, db kvdb.Instance, whole Config, opts ...Option) (*Store, error) {
	switch {
	case h == nil:
		return nil, ErrNoHost
	case db == nil:
		return nil, ErrNoDB
	}
	// A CLOUD THAT ISSUES NO CERTIFICATE STILL PROVES ITS DOMAINS, so the
	// certificate half is checked only when it is on.
	cfg := whole.Certificate
	if cfg.ACME.Enabled {
		var err error
		if cfg, err = cfg.normalised(); err != nil {
			return nil, err
		}
	} else if cfg.Zone = normal(cfg.Zone); cfg.Zone == "" {
		return nil, ErrNoZone
	}
	s := &Store{cfg: cfg, host: h, db: db, now: time.Now, resolver: net.DefaultResolver}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Start answers on Protocol.
func (s *Store) Start(ctx context.Context) error {
	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount())
	if err != nil {
		return err
	}
	s.server = server
	return nil
}

// Close stops the listener.
func (s *Store) Close() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}
