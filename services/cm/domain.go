// The domain half of the cm service: proving a custom domain is a project's,
// as v1 did, with github.com/taubyte/domain-validation.
//
// The cloud signs a claim over (fqdn, project). The owner publishes the token
// as a TXT record at `<first 8 of the project id>.<fqdn>`; the name is the
// project's once a lookup there returns a token this cloud signed for that
// pair. Nobody else can produce one, so the record proves control of the zone.
//
// # One signing key per cm node
//
// Each cm node signs with its own key, kept in the secrets service under
// VaultProject/DomainEnvironment and named for the node, and publishes the
// public half in this service's database at /domain/key/<peer id>. Two nodes
// never write one key (AGENTS.md), and a token verifies against any of them.
package cm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	dv "github.com/taubyte/domain-validation"

	"github.com/taubyte/tau/p2p/rpc"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

// DomainEnvironment scopes the domain signing keys in the vault.
const DomainEnvironment = "domain"

const domainKeyPrefix = "/domain/key/"

var (
	// ErrUnproven is a custom domain whose TXT record carries no claim this
	// cloud signed for the project.
	ErrUnproven = errors.New("cm: the domain is not proven to be the project's")

	// ErrBadClaim is a claim asked for something that cannot be claimed.
	ErrBadClaim = errors.New("cm: a claim needs an fqdn and a project id")
)

// Resolver looks a TXT record up. net.DefaultResolver is one.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// WithVault is where the domain signing key is kept. Without one this node
// signs no claim.
func WithVault(v Vault) Option { return func(s *Service) { s.vault = v } }

// WithResolver replaces net.DefaultResolver. Tests use it.
func WithResolver(r Resolver) Option { return func(s *Service) { s.resolver = r } }

// signer holds this node's key once it is read or made.
type signer struct {
	mu      sync.Mutex
	private []byte
	public  []byte
}

func (s *Service) keyName() string { return "key-" + s.host.ID().String() }

// signingKey is this node's key pair, PEM, made on first use.
func (s *Service) signingKey(ctx context.Context) ([]byte, []byte, error) {
	s.signer.mu.Lock()
	defer s.signer.mu.Unlock()
	if s.signer.private != nil {
		return s.signer.private, s.signer.public, nil
	}
	if s.vault == nil {
		return nil, nil, ErrNoVault
	}
	public, err := s.db.Get(ctx, domainKeyPrefix+s.host.ID().String())
	if err == nil && len(public) > 0 {
		private, err := s.vault.Get(ctx, VaultProject, DomainEnvironment, s.keyName())
		if err != nil {
			return nil, nil, fmt.Errorf("cm: reading the domain key: %w", err)
		}
		s.signer.private, s.signer.public = private, public
		return private, public, nil
	}
	private, public, err := newSigningKey()
	if err != nil {
		return nil, nil, err
	}
	// THE PRIVATE HALF FIRST, so no public key is published whose private one
	// is not kept.
	if err := s.vault.Set(ctx, VaultProject, DomainEnvironment, s.keyName(), private); err != nil {
		return nil, nil, fmt.Errorf("cm: keeping the domain key: %w", err)
	}
	if err := s.db.Put(ctx, domainKeyPrefix+s.host.ID().String(), public); err != nil {
		return nil, nil, fmt.Errorf("cm: publishing the domain key: %w", err)
	}
	s.signer.private, s.signer.public = private, public
	return private, public, nil
}

func newSigningKey() ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), nil
}

// claim signs (fqdn, project) with this node's key.
func (s *Service) claim(ctx context.Context, fqdn, project string) (*cmv2.DomainClaim, error) {
	fqdn = normal(fqdn)
	id, err := cid.Decode(project)
	if fqdn == "" || err != nil {
		return nil, ErrBadClaim
	}
	private, public, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	c, err := dv.New(dv.FQDN(fqdn), dv.Project(id), dv.PrivateKey(private), dv.PublicKey(public))
	if err != nil {
		return nil, err
	}
	token, err := c.Sign()
	if err != nil {
		return nil, err
	}
	return &cmv2.DomainClaim{Entry: dv.TXTEntry(&id, fqdn), Type: "txt", Token: string(token)}, nil
}

// keys is every signing key this node's replica holds.
func (s *Service) keys(ctx context.Context) ([][]byte, error) {
	names, err := s.db.List(ctx, domainKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("cm: listing the domain keys: %w", err)
	}
	slices.Sort(names)
	var out [][]byte
	for _, n := range names {
		if b, err := s.db.Get(ctx, n); err == nil && len(b) > 0 {
			out = append(out, b)
		}
	}
	return out, nil
}

// Prove is nil when fqdn's TXT record carries a claim for project signed by
// one of keys.
func Prove(ctx context.Context, r Resolver, keys [][]byte, project, fqdn string) error {
	fqdn = normal(fqdn)
	id, err := cid.Decode(project)
	if err != nil {
		return fmt.Errorf("%w: %s: project %q: %w", ErrUnproven, fqdn, project, err)
	}
	entry := dv.TXTEntry(&id, fqdn)
	if len(keys) == 0 {
		return fmt.Errorf("%w: %s: this cloud holds no key to check %s against", ErrUnproven, fqdn, entry)
	}
	records, err := r.LookupTXT(ctx, entry)
	if err != nil {
		return fmt.Errorf("%w: %s: no TXT record at %s: %w", ErrUnproven, fqdn, entry, err)
	}
	address := dv.Address(&id, fqdn)
	for _, token := range records {
		for _, key := range keys {
			c, err := dv.FromToken(dv.Token(token), dv.PublicKey(key))
			if err == nil && c.Address == address {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s: the TXT record at %s carries no claim this cloud signed", ErrUnproven, fqdn, entry)
}

func (s *Service) prove(ctx context.Context, project, fqdn string) error {
	keys, err := s.keys(ctx)
	if err != nil {
		return err
	}
	return Prove(ctx, s.resolver, keys, project, fqdn)
}

func (h handler) Claim(ctx context.Context, req *connect.Request[cmv2.ClaimRequest]) (*connect.Response[cmv2.DomainClaim], error) {
	c, err := h.s.claim(ctx, req.Msg.GetFqdn(), req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(c), nil
}

func (h handler) Keys(ctx context.Context, _ *connect.Request[cmv2.Empty]) (*connect.Response[cmv2.ValidationKeys], error) {
	keys, err := h.s.keys(ctx)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&cmv2.ValidationKeys{Public: keys}), nil
}

// Domains issues claims and proves them, against whichever cm nodes answer.
type Domains struct {
	host     host.Host
	members  func() []peer.ID
	local    func() *Service
	resolver Resolver

	mu    sync.Mutex
	conns map[peer.ID]*rpc.Client
}

// NewDomains builds the domain client. members and local are as NewClient's;
// a nil resolver is net.DefaultResolver.
func NewDomains(h host.Host, members func() []peer.ID, local func() *Service, r Resolver) (*Domains, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if r == nil {
		r = net.DefaultResolver
	}
	return &Domains{host: h, members: members, local: local, resolver: r, conns: map[peer.ID]*rpc.Client{}}, nil
}

// Close releases every stream.
func (d *Domains) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, conn := range d.conns {
		conn.Close()
		delete(d.conns, id)
	}
	return nil
}

func (d *Domains) targets() []peer.ID {
	var all []peer.ID
	if d.local() != nil {
		all = append(all, d.host.ID())
	}
	for _, p := range d.members() {
		if p != d.host.ID() && !slices.Contains(all, p) {
			all = append(all, p)
		}
	}
	return all
}

func (d *Domains) at(p peer.ID) (cmv2connect.DomainServiceClient, error) {
	if p == d.host.ID() {
		if s := d.local(); s != nil {
			return handler{s}, nil
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	conn, ok := d.conns[p]
	if !ok {
		var err error
		if conn, err = rpc.NewClient(d.host, p, Protocol); err != nil {
			return nil, err
		}
		d.conns[p] = conn
	}
	return cmv2connect.NewDomainServiceClient(conn.HTTP(), conn.URL()), nil
}

// Claim is the TXT record that proves fqdn is project's.
func (d *Domains) Claim(ctx context.Context, project, fqdn string) (*cmv2.DomainClaim, error) {
	targets := d.targets()
	if len(targets) == 0 {
		return nil, ErrNoMembers
	}
	var errs []error
	for _, p := range targets {
		svc, err := d.at(p)
		if err == nil {
			var res *connect.Response[cmv2.DomainClaim]
			if res, err = svc.Claim(ctx, connect.NewRequest(&cmv2.ClaimRequest{Fqdn: fqdn, Project: project})); err == nil {
				return res.Msg, nil
			}
			if connect.CodeOf(err) == connect.CodeInvalidArgument {
				return nil, ErrBadClaim
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", p, err))
	}
	return nil, errors.Join(errs...)
}

// Prove is nil when fqdn is proven to be project's. The keys are every cm
// node's, so one whose replica is behind hides none.
func (d *Domains) Prove(ctx context.Context, project, fqdn string) error {
	targets := d.targets()
	if len(targets) == 0 {
		return fmt.Errorf("%w: %s: %w", ErrUnproven, fqdn, ErrNoMembers)
	}
	var keys [][]byte
	var errs []error
	for _, p := range targets {
		svc, err := d.at(p)
		if err == nil {
			var res *connect.Response[cmv2.ValidationKeys]
			if res, err = svc.Keys(ctx, connect.NewRequest(&cmv2.Empty{})); err == nil {
				for _, k := range res.Msg.GetPublic() {
					if !slices.ContainsFunc(keys, func(h []byte) bool { return string(h) == string(k) }) {
						keys = append(keys, k)
					}
				}
				continue
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", p, err))
	}
	if len(errs) == len(targets) {
		return fmt.Errorf("%w: %s: %w", ErrUnproven, fqdn, errors.Join(errs...))
	}
	return Prove(ctx, d.resolver, keys, project, fqdn)
}

// under is name, or a name below zone.
func under(zone, name string) bool {
	return zone != "" && (name == zone || strings.HasSuffix(name, "."+zone))
}
