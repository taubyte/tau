package instance

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/dns"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// Wires the one client every public listener takes certificates from, over the cm
// beside it or wherever the finder finds one (services/cm runs from the catalog).
// `cm.certificate.acme` makes every node a client ordering its own. DNS-01 values
// are cache entries over their own replicated database (cm.ChallengeDatabase).

// CM is the service that holds what the cloud keeps once for every
// region.
var CM = cm.Name

// ErrNoZoneForCertificates is ACME on a node that does not know the zone.
var ErrNoZoneForCertificates = errors.New("node: certificates are issued for the cloud's zone, and this node names none (cloudfqdn)")

// certVault is the vault every certificate key goes through.
func (n *Instance) certVault() secretsService.CloudVault {
	n.Vault()
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	return n.shared.Cloud(cm.VaultService)
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
	if n.cfg.CloudFqdn == "" {
		return nil, ErrNoZoneForCertificates
	}
	cfg := n.cfg.CM
	cfg.Certificate.Zone = n.cfg.CloudFqdn
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
		cm.On(&added{node: n}), n.certVault(), cfg, options...)
	if err != nil {
		return nil, err
	}
	n.certs = c
	n.closers = append(n.closers, c)
	return c, nil
}

// ServiceTLS is what a service's own public listener answers handshakes
// with — the plm service's, an added service's: the cloud's certificate
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

// listeners is ServiceTLS as a service on this node asks for it.
type listeners struct{ n *Instance }

func (l listeners) ServiceTLS() (*tls.Config, error) { return l.n.ServiceTLS() }

// challengeStore is the DNS-01 database, opened once per node.
func (n *Instance) challengeStore() (cacheStore, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.challengeDB != nil {
		return cacheStore{n.challengeDB}, nil
	}
	db, err := n.dbs.at(cm.ChallengeDatabase)
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

// WithResolver is where cm looks up the TXT records proving a custom domain.
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
	d, err := cm.NewDomains(n.Host(), cm.On(&added{node: n}))
	if err != nil {
		return nil, err
	}
	n.domainClaims = d
	n.closers = append(n.closers, d)
	return d, nil
}
