package node

import (
	"crypto/tls"
	"errors"
	"strings"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	certificateService "github.com/taubyte/tau/services/certificate"
)

// certificate.go wires the certificate service and the one client every
// public listener on this node takes its certificates from.
//
// # Which nodes issue, and which serve
//
// A node naming `certificate` in its services issues; the cloud's
// `certificate.acme` turning on is what makes every other node a consumer. Its
// gateway is handed the client as its issuer, its dns server answers DNS-01
// through it, and its build frontend serves TLS with it. A node of a cloud
// with no ACME builds no client and serves what it served before.

// Certificate is the service that issues and keeps the cloud's certificates.
var Certificate = certificateService.Name

// ErrNoZoneForCertificates is ACME on a node that does not know the zone.
var ErrNoZoneForCertificates = errors.New("node: certificates are issued for the cloud's zone, and this node's gateway names none (gateway.cloudfqdn)")

// certVault is the vault every certificate key goes through.
func (n *Node) certVault() *vault {
	n.Vault()
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	return n.shared
}

func (n *Node) openCertificate() error {
	if !n.cfg.runs(Certificate) {
		return nil
	}
	if n.opts.databases == nil {
		return ErrNoDatabases
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return ErrNoZoneForCertificates
	}

	db, err := n.opts.databases.New(logger, Certificate, rebroadcastSeconds)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))

	cfg := n.cfg.Certificate
	cfg.Zone = n.cfg.Gateway.CloudFqdn
	options := []certificateService.Option{
		certificateService.WithVault(n.certVault()),
		certificateService.WithPeers(func() []libp2ppeer.ID {
			return (&fromDiscovery{node: n, name: Certificate}).Known()
		}),
		certificateService.WithNames(n.servedNames),
		certificateService.WithLogger(logger.Warnf),
	}
	if n.opts.now != nil {
		options = append(options, certificateService.WithClock(n.opts.now))
	}
	svc, err := certificateService.New(n.opts.host, db, cfg, options...)
	if err != nil {
		return err
	}
	n.certificate = svc
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
func (n *Node) certificates() (*certificateService.Client, error) {
	if !n.cfg.Certificate.ACME.Enabled {
		return nil, nil
	}
	if n.certs != nil {
		return n.certs, nil
	}
	if n.cfg.Gateway.CloudFqdn == "" {
		return nil, ErrNoZoneForCertificates
	}
	c, err := certificateService.NewClient(n.opts.host,
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: Certificate}).Known() },
		func() *certificateService.Service { return n.certificate },
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
