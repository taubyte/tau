package node

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

// cm.go wires the cm service — the store of the cloud's
// certificates, for now — and the one client every public listener on this
// node takes its certificates from.
//
// A node naming `cm` in its services hosts the store; the cloud's
// `cm.certificate.acme` turning on makes every node a client that orders
// what it needs itself. DNS-01 values are cache entries over their own
// replicated database (challengeDatabase), opened by every node that orders
// or answers them.

// CM is the service that holds what the cloud keeps once for every
// region.
var CM = cm.Name

// challengeDatabase is where the answers live while an order is proved.
const challengeDatabase = "cm-challenges"

// ErrNoZoneForCertificates is ACME on a node that does not know the zone.
var ErrNoZoneForCertificates = errors.New("node: certificates are issued for the cloud's zone, and this node's gateway names none (gateway.cloudfqdn)")

// certVault is the vault every certificate key goes through.
func (n *Node) certVault() cloudVault {
	n.Vault()
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	return n.shared.cloud(cm.VaultService)
}

func (n *Node) openCM() error {
	if !n.cfg.runs(CM) {
		return nil
	}
	if n.opts.databases == nil {
		return ErrNoDatabases
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return ErrNoZoneForCertificates
	}

	db, err := n.opts.databases.New(logger, CM, rebroadcastSeconds)
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
	svc, err := cm.New(n.opts.host, db, cfg, options...)
	if err != nil {
		return err
	}
	n.cm = svc
	n.closers = append(n.closers, svc)
	return nil
}

// certificates is the client this node's listeners take certificates from,
// built on first use, or nil in a cloud that issues none.
func (n *Node) certificates() (*cm.Client, error) {
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
	c, err := cm.NewClient(n.opts.host,
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
func (n *Node) ServiceTLS() (*tls.Config, error) {
	c, err := n.certificates()
	if c == nil || err != nil {
		return nil, err
	}
	return c.TLSConfig(), nil
}

// challengeStore is the DNS-01 database, opened once per node.
func (n *Node) challengeStore() (cacheStore, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.challengeDB != nil {
		return cacheStore{n.challengeDB}, nil
	}
	if n.opts.databases == nil {
		return cacheStore{}, ErrNoDatabases
	}
	db, err := n.opts.databases.New(logger, challengeDatabase, rebroadcastSeconds)
	if err != nil {
		return cacheStore{}, err
	}
	n.challengeDB = db
	n.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}

// answers is this node's copy of the challenge store, opened once, or nil on
// a node with no databases.
func (n *Node) answers() (*cm.Challenges, error) {
	if n.opts.databases == nil {
		return nil, nil
	}
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
func (n *Node) challengeReader() (dns.Challenges, error) {
	if !n.cfg.CM.Certificate.ACME.Enabled {
		return nil, nil
	}
	local, err := n.answers()
	if err != nil {
		return nil, err
	}
	if local == nil {
		return nil, ErrNoDatabases
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
func (n *Node) domains() (*cm.Domains, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.domainClaims != nil {
		return n.domainClaims, nil
	}
	d, err := cm.NewDomains(n.opts.host,
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
func (n *Node) domainProver() (deploy.Domains, error) {
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
