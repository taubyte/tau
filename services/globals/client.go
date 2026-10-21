package globals

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	globalsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/globals/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/globals/v2/globalsv2connect"
)

// client.go is how a node that serves TLS, answers HTTP-01 or answers DNS-01
// reaches the globals nodes. It is the one helper every public listener
// takes its certificates from: GetCertificate is a tls.Config's.

// ErrNoMembers is a cloud where no node reports this service.
var ErrNoMembers = errors.New("globals: no certificate node is reachable")

// Keys reads a leaf's private key from the vault.
type Keys interface {
	Get(ctx context.Context, project, environment, name string) ([]byte, error)
}

// Client asks globals nodes, and keeps what they answered.
type Client struct {
	host    host.Host
	members func() []peer.ID
	local   func() *Service
	keys    Keys
	zone    string
	timeout time.Duration
	now     func() time.Time

	mu      sync.Mutex
	cache   map[string]*tls.Certificate
	conns   map[peer.ID]*rpc.Client
	refresh map[string]bool
}

// NewClient builds a client. members is the nodes reporting the service and
// local this node's own, nil when it runs none; both are asked at call time.
func NewClient(h host.Host, members func() []peer.ID, local func() *Service, keys Keys, zone string) (*Client, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if keys == nil {
		return nil, ErrNoVault
	}
	return &Client{
		host: h, members: members, local: local, keys: keys,
		zone:    strings.ToLower(strings.Trim(zone, ".")),
		timeout: 30 * time.Second,
		now:     time.Now,
		cache:   map[string]*tls.Certificate{},
		conns:   map[peer.ID]*rpc.Client{},
		refresh: map[string]bool{},
	}, nil
}

// Close releases every stream.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, conn := range c.conns {
		conn.Close()
		delete(c.conns, id)
	}
	return nil
}

// targets is the members, the owner of name first.
func (c *Client) targets(name string) []peer.ID {
	var all []peer.ID
	if c.local() != nil {
		all = append(all, c.host.ID())
	}
	for _, p := range c.members() {
		if !slices.Contains(all, p) && (p != c.host.ID() || c.local() != nil) {
			all = append(all, p)
		}
	}
	owner, ok := Owner(name, all)
	if !ok {
		return nil
	}
	out := []peer.ID{owner}
	for _, p := range all {
		if p != owner {
			out = append(out, p)
		}
	}
	return out
}

func (c *Client) at(p peer.ID) (globalsv2connect.CertificateServiceClient, error) {
	if p == c.host.ID() {
		if s := c.local(); s != nil {
			return handler{s}, nil
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, ok := c.conns[p]
	if !ok {
		var err error
		if conn, err = rpc.NewClient(c.host, p, Protocol); err != nil {
			return nil, err
		}
		c.conns[p] = conn
	}
	return globalsv2connect.NewCertificateServiceClient(conn.HTTP(), conn.URL()), nil
}

// each calls f on every target in order until one answers.
func (c *Client) each(name string, f func(globalsv2connect.CertificateServiceClient) error) error {
	targets := c.targets(name)
	if len(targets) == 0 {
		return ErrNoMembers
	}
	var errs []error
	for _, p := range targets {
		svc, err := c.at(p)
		if err == nil {
			if err = f(svc); err == nil {
				return nil
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", p, err))
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Code() == connect.CodeInvalidArgument {
			break
		}
	}
	return errors.Join(errs...)
}

// Certificate is the certificate for name, issued when nothing is kept. A
// certificate past two thirds of its life is fetched again in the background
// and keeps answering until a newer one is.
func (c *Client) Certificate(ctx context.Context, name string) (tls.Certificate, error) {
	name = normal(name)
	if !pkgcertificate.Under(c.zone, name) && name != c.zone {
		return tls.Certificate{}, fmt.Errorf("%w: %s is not in %s", ErrNotZone, name, c.zone)
	}
	c.mu.Lock()
	cert := c.cache[name]
	c.mu.Unlock()
	if cert != nil && c.now().Before(cert.Leaf.NotAfter) {
		if c.stale(cert.Leaf) {
			c.refreshInBackground(name)
		}
		return *cert, nil
	}
	fetched, err := c.fetch(ctx, name)
	if err != nil {
		return tls.Certificate{}, err
	}
	return *fetched, nil
}

func (c *Client) stale(leaf *x509.Certificate) bool {
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(c.now()) < lifetime/3
}

func (c *Client) refreshInBackground(name string) {
	c.mu.Lock()
	if c.refresh[name] {
		c.mu.Unlock()
		return
	}
	c.refresh[name] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.refresh, name)
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		c.fetch(ctx, name)
	}()
}

func (c *Client) fetch(ctx context.Context, name string) (*tls.Certificate, error) {
	var issued *globalsv2.Issued
	err := c.each(name, func(svc globalsv2connect.CertificateServiceClient) error {
		res, err := svc.Certificate(ctx, connect.NewRequest(&globalsv2.CertificateRequest{Name: name, Issue: true}))
		if err == nil {
			issued = res.Msg
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	ref := issued.GetKey()
	key, err := c.keys.Get(ctx, ref.GetProject(), ref.GetEnvironment(), ref.GetName())
	if err != nil {
		return nil, fmt.Errorf("globals: opening the key of %s: %w", name, err)
	}
	cert, err := pkgcertificate.KeyPair(append(append([]byte(nil), issued.GetChain()...), key...))
	if err != nil {
		return nil, err
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, err
		}
	}
	if err := cert.Leaf.VerifyHostname(name); err != nil {
		return nil, fmt.Errorf("globals: what is kept for %s does not name it: %w", name, err)
	}
	c.mu.Lock()
	c.cache[name] = &cert
	c.mu.Unlock()
	return &cert, nil
}

// GetCertificate is a tls.Config's: the certificate for the handshake's name.
func (c *Client) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := normal(hello.ServerName)
	if name == "" {
		return nil, errors.New("globals: the handshake names no server")
	}
	ctx := hello.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cert, err := c.Certificate(ctx, name)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// TLSConfig serves every name in the zone with what this client fetches.
func (c *Client) TLSConfig() *tls.Config {
	return &tls.Config{GetCertificate: c.GetCertificate, MinVersion: tls.VersionTLS12}
}

// Challenge is the HTTP-01 answer for token, from the certificate nodes. A
// node holding its own copy of the challenge store answers from that
// instead (Challenges.Challenge).
func (c *Client) Challenge(token string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	values, err := c.values(ctx, acme.KindHTTP, token)
	if err != nil || len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// Challenges are the DNS-01 values for fqdn, from the certificate nodes.
func (c *Client) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	return c.values(ctx, acme.KindDNS, fqdn)
}

// values asks every certificate node: the one proving the name wrote them,
// and asking all of them is what makes that one always among those asked. A
// node that did not answer leaves the set uncertain, so nothing found with
// one silent is an error and not an empty answer.
func (c *Client) values(ctx context.Context, kind, name string) ([]string, error) {
	targets := c.targets(name)
	if len(targets) == 0 {
		return nil, ErrNoMembers
	}
	type answer struct {
		values []string
		err    error
	}
	answers := make(chan answer, len(targets))
	for _, p := range targets {
		go func() {
			svc, err := c.at(p)
			if err != nil {
				answers <- answer{err: err}
				return
			}
			res, err := svc.Challenges(ctx, connect.NewRequest(&globalsv2.ChallengesRequest{Kind: kind, Name: name}))
			if err != nil {
				answers <- answer{err: err}
				return
			}
			answers <- answer{values: res.Msg.GetValues()}
		}()
	}
	var out []string
	var errs []error
	for range targets {
		a := <-answers
		if a.err != nil {
			errs = append(errs, a.err)
			continue
		}
		for _, v := range a.values {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}
