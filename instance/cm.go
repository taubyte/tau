package instance

import (
	"context"
	"crypto/tls"
	"errors"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/dns"
)

// Wires the cm service (the cloud's certificate store) and the one client every
// public listener takes certificates from. A node naming `cm` hosts the store;
// `cm.certificate.acme` makes every node a client ordering its own. DNS-01 values
// are cache entries over their own replicated database (challengeDatabase).

// CM is the service that holds what the cloud keeps once for every
// region.
var CM = cm.Name

// challengeDatabase is where the answers live while an order is proved.
const challengeDatabase = "cm-challenges"

// ErrNoZoneForCertificates is ACME on a node that does not know the zone.
var ErrNoZoneForCertificates = errors.New("node: certificates are issued for the cloud's zone, and this node's gateway names none (gateway.cloudfqdn)")

// certVault is the vault every certificate key goes through.
func (n *Instance) certVault() cloudVault {
	n.Vault()
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	return n.shared.cloud(cm.VaultService)
}

func (n *Instance) openCM() error {
	if !n.cfg.runs(CM) {
		return nil
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return ErrNoZoneForCertificates
	}

	db, err := n.kvdb(CM)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))
	// A HOLDER OF THE CHALLENGE STORE TOO, so DNS-01 values replicate through
	// a node that is always up.
	if _, err := n.answers(); err != nil {
		return err
	}

	cfg := n.cfg.CM
	cfg.Certificate.Zone = n.cfg.Gateway.CloudFqdn
	options := []cm.Option{cm.WithVault(n.certVault())}
	if n.opts.resolver != nil {
		options = append(options, cm.WithResolver(n.opts.resolver))
	}
	if n.opts.now != nil {
		options = append(options, cm.WithClock(n.opts.now))
	}
	svc, err := cm.New(n.Host(), db, cfg, options...)
	if err != nil {
		return err
	}
	n.cm = svc
	n.closers = append(n.closers, svc)
	return nil
}

// certificates is the client this node's listeners take certificates from,
// built on first use, or nil in a cloud that issues none.
func (n *Instance) certificates() (*cm.Client, error) {
	if !n.cfg.CM.Certificate.ACME.Enabled {
		return nil, nil
	}
	if n.certs != nil {
		return n.certs, nil
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return nil, ErrNoZoneForCertificates
	}
	cfg := n.cfg.CM
	cfg.Certificate.Zone = n.cfg.Gateway.CloudFqdn
	options := []cm.ClientOption{cm.WithLogger(logger.Warnf)}
	if n.opts.now != nil {
		options = append(options, cm.WithClientClock(n.opts.now))
	}
	local, err := n.answers()
	if err != nil {
		return nil, err
	}
	if local != nil {
		options = append(options, cm.WithChallengeStore(local))
	}
	d, err := n.domains()
	if err != nil {
		return nil, err
	}
	options = append(options, cm.WithProver(d))
	c, err := cm.NewClient(n.Host(),
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: CM}).Known() },
		func() *cm.Service { return n.cm },
		n.certVault(), cfg, options...)
	if err != nil {
		return nil, err
	}
	n.certs = c
	n.closers = append(n.closers, c)
	return c, nil
}

// ServiceTLS is what a service's own public listener answers handshakes
// with — the build frontend's, an added service's: the cloud's certificate
// for `<service>.<zone>` (the zone's wildcard, when it issues one), or nil
// when the cloud issues none. The gateway's listener takes TLSConfig, which
// asks the gateway serving each name first.
func (n *Instance) ServiceTLS() (*tls.Config, error) {
	c, err := n.certificates()
	if c == nil || err != nil {
		return nil, err
	}
	return c.TLSConfig(), nil
}

// challengeStore is the DNS-01 database, opened once per node.
func (n *Instance) challengeStore() (cacheStore, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.challengeDB != nil {
		return cacheStore{n.challengeDB}, nil
	}
	db, err := n.kvdb(challengeDatabase)
	if err != nil {
		return cacheStore{}, err
	}
	n.challengeDB = db
	n.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}

// answers is this instance's copy of the challenge store, opened once.
func (n *Instance) answers() (*cm.Challenges, error) {
	st, err := n.challengeStore()
	if err != nil {
		return nil, err
	}
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.challengeAnswers == nil {
		if n.challengeAnswers, err = cm.ChallengesIn(context.Background(), st, n.clock); err != nil {
			return nil, err
		}
	}
	return n.challengeAnswers, nil
}

// challengeReader is what this node's dns server answers DNS-01 with, or nil
// in a cloud that issues none.
func (n *Instance) challengeReader() (dns.Challenges, error) {
	if !n.cfg.CM.Certificate.ACME.Enabled {
		return nil, nil
	}
	local, err := n.answers()
	if err != nil {
		return nil, err
	}
	return local, nil
}

// WithResolver is where TXT records proving a custom domain are looked up.
// Without it, net.DefaultResolver.
func WithResolver(r cm.Resolver) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: nil resolver")
		}
		o.resolver = r
		return nil
	}
}

// domains is the client that issues and proves domain claims, built once.
func (n *Instance) domains() (*cm.Domains, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.domainClaims != nil {
		return n.domainClaims, nil
	}
	d, err := cm.NewDomains(n.Host(),
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: CM}).Known() },
		func() *cm.Service { return n.cm })
	if err != nil {
		return nil, err
	}
	n.domainClaims = d
	n.closers = append(n.closers, d)
	return d, nil
}

// trustedDomains is the prover of a cloud whose zones are trusted unproven.
type trustedDomains struct{}

func (trustedDomains) Prove(context.Context, string, string) error { return nil }

// domainProver is what this node's deploys prove a zone with.
func (n *Instance) domainProver() (deploy.Domains, error) {
	if n.cfg.CM.Domains.Trusted {
		return trustedDomains{}, nil
	}
	return n.domains()
}

// domainClaims is the claim route's seam over the cm client.
type domainClaims struct{ d *cm.Domains }

func (c domainClaims) Claim(ctx context.Context, project, fqdn string) (frontend.DomainClaim, error) {
	claim, err := c.d.Claim(ctx, project, fqdn)
	if errors.Is(err, cm.ErrBadClaim) {
		return frontend.DomainClaim{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return frontend.DomainClaim{}, err
	}
	return frontend.DomainClaim{Entry: claim.GetEntry(), Type: claim.GetType(), Token: claim.GetToken()}, nil
}
