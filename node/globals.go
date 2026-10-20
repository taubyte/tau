package node

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/services/globals"
)

// globals.go wires the globals service — the cloud's certificates, for now —
// and the one client every public listener on this node takes its
// certificates from.
//
// # Which nodes issue, and which serve
//
// A node naming `globals` in its services issues; the cloud's
// `globals.certificate.acme` turning on is what makes every other node a
// consumer. Its gateway is handed the client as its issuer, its dns server
// answers DNS-01 through it, and its build frontend serves TLS with it. A node
// of a cloud with no ACME builds no client and serves what it served before.
//
// DNS-01 values are cache entries over their own replicated database
// (challengeDatabase), opened by every globals node and every dns node, so a
// dns node answers from its own copy and asks the globals nodes only when it
// holds nothing yet.

// Globals is the service that holds what the cloud keeps once for every
// region.
var Globals = globals.Name

// challengeDatabase is where DNS-01 values live while an order is proved.
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

	challenges, err := n.challengeStore()
	if err != nil {
		return err
	}
	cfg := n.cfg.Globals
	cfg.Certificate.Zone = n.cfg.Gateway.CloudFqdn
	options := []globals.Option{
		globals.WithChallenges(challenges),
		globals.WithVault(n.certVault()),
		globals.WithPeers(func() []libp2ppeer.ID {
			return (&fromDiscovery{node: n, name: Globals}).Known()
		}),
		globals.WithNames(n.servedNames),
		globals.WithLogger(logger.Warnf),
	}
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

// servedNames is `<service>.<zone>` for every service a node reports now.
func (n *Node) servedNames() []string {
	zone := strings.Trim(n.cfg.Gateway.CloudFqdn, ".")
	var out []string
	for _, name := range n.knownServices() {
		if len((&fromDiscovery{node: n, name: name}).Known()) > 0 {
			out = append(out, name+"."+zone)
		}
	}
	return out
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
	c, err := globals.NewClient(n.opts.host,
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: Globals}).Known() },
		func() *globals.Service { return n.globals },
		n.certVault(), n.cfg.Gateway.CloudFqdn)
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

// dnsChallenges answers DNS-01 from this node's copy of the values, and from
// the globals nodes while it holds none: a value just presented may not have
// reached this node yet, and the node that presented it always holds it.
type dnsChallenges struct {
	local  *globals.Challenges
	remote *globals.Client
}

func (d dnsChallenges) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	if d.local != nil {
		if values, err := d.local.Challenges(ctx, fqdn); err == nil && len(values) > 0 {
			return values, nil
		}
	}
	return d.remote.Challenges(ctx, fqdn)
}

// challengeReader is what this node's dns server answers DNS-01 with, or nil
// in a cloud that issues none.
func (n *Node) challengeReader() (*dnsChallenges, error) {
	c, err := n.certificates()
	if c == nil || err != nil {
		return nil, err
	}
	reader := &dnsChallenges{remote: c}
	if n.opts.databases != nil {
		st, err := n.challengeStore()
		if err != nil {
			return nil, err
		}
		local, err := globals.ChallengesIn(context.Background(), st, n.clock)
		if err != nil {
			return nil, err
		}
		reader.local = local
	}
	return reader, nil
}
