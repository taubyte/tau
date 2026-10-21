package node

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/services/dns"
	"github.com/taubyte/tau/services/gateway"
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
// DNS-01 and HTTP-01 answers are cache entries over their own replicated
// database (challengeDatabase), opened by every globals, dns and gateway
// node. A dns node and a gateway answer from their own copy, and serve
// globals.ResponderProtocol over it, so the node placing an order has the CA
// look only once every one of them holds the answer.

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
		globals.WithResponders(func(kind string) []libp2ppeer.ID {
			role := DNS
			if kind == acme.KindHTTP {
				role = Gateway
			}
			return (&fromDiscovery{node: n, name: role}).Known()
		}),
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

// challengeReader is what this node's dns server answers DNS-01 with — its
// own copy of the store, which the issuer confirmed holds the value before
// the CA looks; the globals nodes on a node with none — or nil in a cloud
// that issues none.
func (n *Node) challengeReader() (dns.Challenges, error) {
	c, err := n.certificates()
	if c == nil || err != nil {
		return nil, err
	}
	local, err := n.answers()
	if err != nil {
		return nil, err
	}
	if local != nil {
		return local, nil
	}
	return c, nil
}

// gatewayIssuer answers HTTP-01 from this node's copy of the store.
type gatewayIssuer struct {
	*globals.Client
	local *globals.Challenges
}

func (g gatewayIssuer) Challenge(token string) (string, bool) { return g.local.Challenge(token) }

// gatewayIssuer is the client, answering HTTP-01 from this node's own copy
// of the store when it has one.
func (n *Node) gatewayIssuer(c *globals.Client) (gateway.Issuer, error) {
	local, err := n.answers()
	if err != nil || local == nil {
		return c, err
	}
	return gatewayIssuer{Client: c, local: local}, nil
}

// serveResponder answers globals.ResponderProtocol on a dns node or gateway
// of a cloud that issues.
func (n *Node) serveResponder(ctx context.Context) error {
	if !n.cfg.Globals.Certificate.ACME.Enabled || (!n.cfg.runs(DNS) && !n.cfg.runs(Gateway)) {
		return nil
	}
	local, err := n.answers()
	if err != nil || local == nil {
		return err
	}
	r, err := globals.ServeResponder(ctx, n.opts.host, local)
	if err != nil {
		return err
	}
	n.addCloser(r)
	return nil
}
