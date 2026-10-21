package cm

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sync/singleflight"

	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

// client.go is how a node gets its certificates: it reads the newest one kept
// on the cm nodes, and orders one from the CA itself when none is. It is
// the one helper every public listener takes its certificates from:
// GetCertificate is a tls.Config's.

// CertWait bounds how long a node finding nothing kept waits for a
// certificate another node may be ordering, before ordering one itself.
const CertWait = 3 * time.Second

// Recheck is how long a certificate is served before the store is read
// again, so nodes that ordered one name at once converge on the newest.
const Recheck = time.Minute

// ErrNoMembers is a cloud where no node reports this service.
var ErrNoMembers = errors.New("cm: no cm node is reachable")

// Vault is the slice of the secrets service the keys are kept in.
type Vault interface {
	Get(ctx context.Context, project, environment, name string) ([]byte, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
}

type cached struct {
	cert   *tls.Certificate
	looked time.Time
}

// Client reads, orders and keeps the cloud's certificates.
type Client struct {
	host       host.Host
	members    func() []peer.ID
	local      func() *Service
	vault      Vault
	cfg        CertificateConfig
	challenges *Challenges
	timeout    time.Duration
	wait       time.Duration
	poll       time.Duration
	now        func() time.Time
	logf       func(string, ...any)

	mu      sync.Mutex
	cache   map[string]*cached
	conns   map[peer.ID]*rpc.Client
	refresh map[string]bool
	flight  singleflight.Group

	keyMu   sync.Mutex
	account crypto.Signer
	issuer  *acme.Issuer

	domains Prover
	custom  map[string]custom
}

// Prover proves a custom domain is a project's. Domains is one.
type Prover interface {
	Prove(ctx context.Context, project, fqdn string) error
}

// custom is the project and zone a name outside the cloud's zone is ordered for.
type custom struct{ project, zone string }

// WithProver is what proves a custom zone before a name in it is ordered.
// Without one no name outside the cloud's zone is.
func WithProver(p Prover) ClientOption { return func(c *Client) { c.domains = p } }

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithChallengeStore is where this node presents its DNS-01 values: its copy
// of the replicated challenge store. Required to order by DNS-01.
func WithChallengeStore(c *Challenges) ClientOption {
	return func(cl *Client) { cl.challenges = c }
}

// WithClientClock injects the clock. Tests use it.
func WithClientClock(now func() time.Time) ClientOption {
	return func(c *Client) { c.now = now }
}

// WithCertWait replaces CertWait. Tests use it.
func WithCertWait(d time.Duration) ClientOption { return func(c *Client) { c.wait = d } }

// WithLogger is where what no caller waits on is said.
func WithLogger(logf func(string, ...any)) ClientOption {
	return func(c *Client) { c.logf = logf }
}

// NewClient builds a client. members is the nodes reporting the service and
// local this node's own, nil when it runs none; both are asked at call time.
func NewClient(h host.Host, members func() []peer.ID, local func() *Service, v Vault, whole Config, opts ...ClientOption) (*Client, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if v == nil {
		return nil, ErrNoVault
	}
	cfg, err := whole.Certificate.normalised()
	if err != nil {
		return nil, err
	}
	c := &Client{
		host: h, members: members, local: local, vault: v, cfg: cfg,
		timeout: 30 * time.Second,
		wait:    CertWait,
		poll:    250 * time.Millisecond,
		now:     time.Now,
		logf:    func(string, ...any) {},
		cache:   map[string]*cached{},
		conns:   map[peer.ID]*rpc.Client{},
		refresh: map[string]bool{},
		custom:  map[string]custom{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
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

// targets is every cm node, this one first when it runs one.
func (c *Client) targets() []peer.ID {
	var all []peer.ID
	if c.local() != nil {
		all = append(all, c.host.ID())
	}
	for _, p := range c.members() {
		if !slices.Contains(all, p) && p != c.host.ID() {
			all = append(all, p)
		}
	}
	return all
}

func (c *Client) at(p peer.ID) (cmv2connect.CertificateServiceClient, error) {
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
	return cmv2connect.NewCertificateServiceClient(conn.HTTP(), conn.URL()), nil
}

// stored is the newest issuance of name any cm node holds: each holds a
// replica, and one behind the others must not hide a newer one.
func (c *Client) stored(ctx context.Context, name string) (*kept, error) {
	targets := c.targets()
	if len(targets) == 0 {
		return nil, ErrNoMembers
	}
	var best *kept
	var errs []error
	for _, p := range targets {
		svc, err := c.at(p)
		if err == nil {
			var res *connect.Response[cmv2.Issued]
			if res, err = svc.Certificate(ctx, connect.NewRequest(&cmv2.CertificateRequest{Name: name})); err == nil {
				k, perr := parseRecord(res.Msg)
				if perr == nil && c.now().Before(k.leaf.NotAfter) && (best == nil || k.leaf.NotAfter.After(best.leaf.NotAfter)) {
					best = k
				}
				continue
			}
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	if best != nil {
		return best, nil
	}
	if len(errs) == len(targets) {
		return nil, errors.Join(errs...)
	}
	return nil, fmt.Errorf("%w: %s", ErrNotKept, name)
}

// Certificate is the certificate for name, ordered when nothing is kept. One
// past two thirds of its life is renewed in the background and keeps
// answering until a newer one is.
func (c *Client) Certificate(ctx context.Context, name string) (tls.Certificate, error) {
	name = normal(name)
	if !c.cfg.inZone(name) {
		return tls.Certificate{}, fmt.Errorf("%w: %s is not in %s", ErrNotZone, name, c.cfg.Zone)
	}
	return c.certificate(ctx, c.cfg.served(name))
}

// ForProject is the certificate for name in project's custom zone, ordered
// only while the zone is proven to be the project's.
func (c *Client) ForProject(ctx context.Context, project, zone, name string) (tls.Certificate, error) {
	name, zone = normal(name), normal(zone)
	if c.cfg.inZone(name) {
		return c.Certificate(ctx, name)
	}
	if !under(zone, name) {
		return tls.Certificate{}, fmt.Errorf("%w: %s is not in %s", ErrNotZone, name, zone)
	}
	if c.domains == nil {
		return tls.Certificate{}, fmt.Errorf("%w: %s: this node proves no domain", ErrUnproven, zone)
	}
	c.mu.Lock()
	c.custom[name] = custom{project: project, zone: zone}
	c.mu.Unlock()
	return c.certificate(ctx, name)
}

func (c *Client) certificate(ctx context.Context, name string) (tls.Certificate, error) {
	c.mu.Lock()
	hit := c.cache[name]
	c.mu.Unlock()
	if hit != nil && c.now().Before(hit.cert.Leaf.NotAfter) {
		if c.due(hit.cert.Leaf) || c.now().Sub(hit.looked) >= Recheck {
			c.refreshInBackground(name)
		}
		return *hit.cert, nil
	}
	res := c.flight.DoChan(name, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), acme.DefaultTimeout+c.wait+c.timeout)
		defer cancel()
		return c.obtain(ctx, name)
	})
	select {
	case <-ctx.Done():
		return tls.Certificate{}, fmt.Errorf("cm: %s: %w", name, ctx.Err())
	case r := <-res:
		if r.Err != nil {
			return tls.Certificate{}, r.Err
		}
		return *r.Val.(*tls.Certificate), nil
	}
}

func (c *Client) due(leaf *x509.Certificate) bool {
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
		ctx, cancel := context.WithTimeout(context.Background(), acme.DefaultTimeout+c.timeout)
		defer cancel()
		if _, err := c.obtain(ctx, name); err != nil {
			c.logf("cm: renewing %s: %v", name, err)
		}
	}()
}

// obtain is the newest kept certificate for name, ordered by this node when
// none is kept or the newest is due.
func (c *Client) obtain(ctx context.Context, name string) (*tls.Certificate, error) {
	k, err := c.stored(ctx, name)
	if errors.Is(err, ErrNotKept) {
		// ANOTHER NODE MAY BE ORDERING IT: wait a bounded while for its
		// issuance before ordering a second.
		deadline := c.now().Add(c.wait)
		for errors.Is(err, ErrNotKept) && c.now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.poll):
			}
			k, err = c.stored(ctx, name)
		}
	}
	if err != nil && !errors.Is(err, ErrNotKept) {
		return nil, err
	}
	if k != nil && !c.due(k.leaf) {
		return c.open(ctx, name, k)
	}
	c.mu.Lock()
	owner, isCustom := c.custom[name]
	c.mu.Unlock()
	var ierr error
	if isCustom {
		ierr = c.domains.Prove(ctx, owner.project, owner.zone)
	}
	var issuer *acme.Issuer
	if ierr == nil {
		issuer, ierr = c.acmeIssuer(ctx)
	}
	if ierr == nil {
		_, ierr = issuer.Issue(name)
	}
	if ierr != nil {
		if k != nil {
			c.logf("cm: renewing %s: %v", name, ierr)
			return c.open(ctx, name, k)
		}
		return nil, ierr
	}
	// THE NEWEST WINS, which is this order's or one another node kept since.
	if k, err = c.stored(ctx, name); err != nil {
		return nil, err
	}
	return c.open(ctx, name, k)
}

// open reads k's key from the vault and caches the pair.
func (c *Client) open(ctx context.Context, name string, k *kept) (*tls.Certificate, error) {
	ref := k.record.GetKey()
	key, err := c.vault.Get(ctx, ref.GetProject(), ref.GetEnvironment(), ref.GetName())
	if err != nil {
		return nil, fmt.Errorf("cm: opening the key of %s: %w", name, err)
	}
	cert, err := pkgcertificate.KeyPair(append(append([]byte(nil), k.record.GetChain()...), key...))
	if err != nil {
		return nil, err
	}
	cert.Leaf = k.leaf
	c.mu.Lock()
	c.cache[name] = &cached{cert: &cert, looked: c.now()}
	c.mu.Unlock()
	return &cert, nil
}

// acmeIssuer is this node's issuer, built on first use.
func (c *Client) acmeIssuer(ctx context.Context) (*acme.Issuer, error) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if c.issuer != nil {
		return c.issuer, nil
	}
	account, err := c.accountLocked(ctx)
	if err != nil {
		return nil, err
	}
	opts := []acme.Option{
		acme.WithStore(keeper{c}),
		acme.WithAccountKey(account),
		acme.WithClock(c.now),
		acme.WithLogger(c.logf),
	}
	if c.cfg.Challenge == ChallengeDNS {
		if c.challenges == nil {
			return nil, ErrNoChallenges
		}
		opts = append(opts, acme.WithDNS(c.cfg.Zone, c.challenges))
	}
	issuer, err := acme.New(c.cfg.ACME, "", opts...)
	if err != nil {
		return nil, err
	}
	c.issuer = issuer
	return issuer, nil
}

// accountLocked is the cloud's ACME account key, read from the vault once it
// is there. c.keyMu is held.
func (c *Client) accountLocked(ctx context.Context) (crypto.Signer, error) {
	if c.account != nil {
		return c.account, nil
	}
	blob, err := c.vault.Get(ctx, VaultProject, VaultEnvironment, AccountSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoAccount, err)
	}
	key, err := acme.ParseKey(blob)
	if err != nil {
		return nil, err
	}
	c.account = key
	return key, nil
}

// Challenge is the HTTP-01 answer for token, from the account key alone.
func (c *Client) Challenge(token string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	c.keyMu.Lock()
	account, err := c.accountLocked(ctx)
	c.keyMu.Unlock()
	if err != nil {
		return "", false
	}
	thumbprint, err := acme.Thumbprint(account)
	if err != nil {
		return "", false
	}
	return acme.Answer(thumbprint, token)
}

// GetCertificate is a tls.Config's: the certificate for the handshake's name.
func (c *Client) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := normal(hello.ServerName)
	if name == "" {
		return nil, errors.New("cm: the handshake names no server")
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

// TLSConfig serves every name in the zone with what this client holds.
func (c *Client) TLSConfig() *tls.Config {
	return &tls.Config{GetCertificate: c.GetCertificate, MinVersion: tls.VersionTLS12}
}

// keeper is the issuer's Store: the key into the vault first and the chain
// onto a cm node after, so no chain is kept whose key is not.
type keeper struct{ c *Client }

// Load is never asked: the client decides what to order itself.
func (keeper) Load(context.Context, string) ([]byte, error) { return nil, acme.ErrMiss }

func (st keeper) Keep(ctx context.Context, name string, blob []byte) error {
	c := st.c
	var chain, key []byte
	for rest := blob; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			chain = append(chain, pem.EncodeToMemory(block)...)
		case strings.HasSuffix(block.Type, "PRIVATE KEY"):
			key = pem.EncodeToMemory(block)
		}
	}
	if chain == nil || key == nil {
		return errors.New("cm: an issuance holds no chain or no key")
	}
	ref := &cmv2.SecretRef{Project: VaultProject, Environment: VaultEnvironment, Name: "leaf-" + issuanceID()}
	if err := c.vault.Set(ctx, ref.Project, ref.Environment, ref.Name, key); err != nil {
		return fmt.Errorf("cm: keeping the key of %s: %w", name, err)
	}
	rec := &cmv2.Issued{Name: name, Chain: chain, Key: ref}
	c.mu.Lock()
	if owner, ok := c.custom[name]; ok {
		rec.Project, rec.Zone = owner.project, owner.zone
	}
	c.mu.Unlock()
	targets := c.targets()
	if len(targets) == 0 {
		return ErrNoMembers
	}
	var errs []error
	for _, p := range targets {
		svc, err := c.at(p)
		if err == nil {
			if _, err = svc.Keep(ctx, connect.NewRequest(rec)); err == nil {
				return nil
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", p, err))
	}
	return errors.Join(errs...)
}

func issuanceID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
