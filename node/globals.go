package node

import (
	"context"
	"crypto/tls"
	"errors"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/services/dns"
	"github.com/taubyte/tau/services/globals"
)

// globals.go wires the globals service — the store of the cloud's
// certificates, for now — and the one client every public listener on this
// node takes its certificates from.
//
// A node naming `globals` in its services hosts the store; the cloud's
// `globals.certificate.acme` turning on makes every node a client that orders
// what it needs itself. DNS-01 values are cache entries over their own
// replicated database (challengeDatabase), opened by every node that orders
// or answers them.

// Globals is the service that holds what the cloud keeps once for every
// region.
var Globals = globals.Name

// challengeDatabase is where the answers live while an order is proved.
const challengeDatabase = "globals-challenges"

// ErrNoZoneForCertificates is ACME on a node that does not know the zone.
var ErrNoZoneForCertificates = errors.New("node: certificates are issued for the cloud's zone, and this node's gateway names none (gateway.cloudfqdn)")

// certVault is the vault every certificate key goes through.
func (n *Node) certVault() *vault {
	n.Vault()
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	return n.shared
}

func (n *Node) openGlobals() error {
	if !n.cfg.runs(Globals) {
		return nil
	}
	if n.opts.databases == nil {
		return ErrNoDatabases
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return ErrNoZoneForCertificates
	}

	db, err := n.opts.databases.New(logger, Globals, rebroadcastSeconds)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))
	// A HOLDER OF THE CHALLENGE STORE TOO, so DNS-01 values replicate through
	// a node that is always up.
	if _, err := n.answers(); err != nil {
		return err
	}

	cfg := n.cfg.Globals
	cfg.Certificate.Zone = n.cfg.Gateway.CloudFqdn
	var options []globals.Option
	if n.opts.now != nil {
		options = append(options, globals.WithClock(n.opts.now))
	}
	svc, err := globals.New(n.opts.host, db, cfg, options...)
	if err != nil {
		return err
	}
	n.globals = svc
	n.closers = append(n.closers, svc)
	return nil
}

// certificates is the client this node's listeners take certificates from,
// built on first use, or nil in a cloud that issues none.
func (n *Node) certificates() (*globals.Client, error) {
	if !n.cfg.Globals.Certificate.ACME.Enabled {
		return nil, nil
	}
	if n.certs != nil {
		return n.certs, nil
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return nil, ErrNoZoneForCertificates
	}
	cfg := n.cfg.Globals
	cfg.Certificate.Zone = n.cfg.Gateway.CloudFqdn
	options := []globals.ClientOption{globals.WithLogger(logger.Warnf)}
	if n.opts.now != nil {
		options = append(options, globals.WithClientClock(n.opts.now))
	}
	local, err := n.answers()
	if err != nil {
		return nil, err
	}
	if local != nil {
		options = append(options, globals.WithChallengeStore(local))
	}
	c, err := globals.NewClient(n.opts.host,
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: Globals}).Known() },
		func() *globals.Service { return n.globals },
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
func (n *Node) answers() (*globals.Challenges, error) {
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
		if n.challengeAnswers, err = globals.ChallengesIn(context.Background(), st, n.clock); err != nil {
			return nil, err
		}
	}
	return n.challengeAnswers, nil
}

// challengeReader is what this node's dns server answers DNS-01 with, or nil
// in a cloud that issues none.
func (n *Node) challengeReader() (dns.Challenges, error) {
	if !n.cfg.Globals.Certificate.ACME.Enabled {
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
