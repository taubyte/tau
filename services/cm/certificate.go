// The certificate half of the cm service.
//
// # What is kept, and where
//
// A certificate's chain is public and lives in this service's own database,
// one key per issuance (/cert/<name>/<not after, 20 digits>/<issuance id>), so
// two issuances never overwrite each other and the newest is the last key in
// byte order. Its private key and the ACME account key are secrets and live in
// the secrets service, under VaultProject.
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
// One account for the whole cloud: the key configured as acme.accountkey,
// the same on every node. Without one cm refuses to start. HTTP-01 therefore
// stores nothing: any gateway answers any token from the account's thumbprint.
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
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
)

// VaultProject and VaultEnvironment scope the keys kept in the secrets
// service. A project name cannot start with "@", so no project's secret is
// ever one of these.
const (
	VaultProject     = "@cloud"
	VaultEnvironment = "certificate"
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
	ErrNoAccount    = errors.New("cm: issuance needs the cloud's ACME account key; set certificate.acme.accountkey")
	ErrDisabled     = acme.ErrDisabled
)

// Config is what an operator writes under the node's `cm:`.
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
}

// normalised checks c and fills its defaults.
func (c CertificateConfig) normalised() (CertificateConfig, error) {
	if err := acme.Check(c.ACME); err != nil {
		return c, fmt.Errorf("cm: %w", err)
	}
	if c.ACME.AccountKey == "" {
		return c, ErrNoAccount
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

// Option configures a Service.
type Option func(*Service)

// WithClock injects the clock. Tests use it.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// Service hosts the certificate store.
type Service struct {
	cfg    CertificateConfig
	host   host.Host
	db     kvdb.KVDB
	now    func() time.Time
	server *rpc.Server
}

// New builds a service.
func New(h host.Host, db kvdb.KVDB, whole Config, opts ...Option) (*Service, error) {
	switch {
	case h == nil:
		return nil, ErrNoHost
	case db == nil:
		return nil, ErrNoDB
	}
	cfg, err := whole.Certificate.normalised()
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, host: h, db: db, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Start answers on Protocol.
func (s *Service) Start(ctx context.Context) error {
	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount())
	if err != nil {
		return err
	}
	s.server = server
	return nil
}

// Close stops the listener.
func (s *Service) Close() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}
