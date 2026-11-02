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
// VaultService and named for the node, and publishes the
// public half in this service's database at /domain/key/<peer id>. Two nodes
// never write one key (AGENTS.md), and a token verifies against any of them.
//
// # Checked at deploy and at issuance, per zone
//
// Each check records its outcome at /domain/proven/<zone>/<project>/<when>;
// serving reads the standing and never looks DNS up. A name under a proven
// zone needs no record of its own, but the zone's record is checked again,
// so a domain released is lost at its next deploy or renewal and the next
// owner's proof takes it. Another project's names under it are refused.
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
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	dv "github.com/taubyte/domain-validation"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

const (
	domainKeyPrefix    = "/domain/key/"
	domainProvenPrefix = "/domain/proven/"
)

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
		private, err := s.vault.Get(ctx, s.keyName())
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
	if err := s.vault.Set(ctx, s.keyName(), private); err != nil {
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

// The outcomes a check records.
const (
	outcomeProven = "proven"
	outcomeLost   = "lost"
)

// provenOwner is the project a zone is proven for now. Each check of a
// (zone, project) records its outcome under a key of its own, so the latest
// per pair is its standing; of the pairs standing proven, the one whose proof
// is oldest owns the zone, the project id breaking a tie, so every node reads
// the same owner from the same replicated entries.
func (s *Service) provenOwner(ctx context.Context, zone string) (string, bool, error) {
	prefix := domainProvenPrefix + zone + "/"
	names, err := s.db.List(ctx, prefix)
	if err != nil {
		return "", false, fmt.Errorf("cm: listing the proofs of %s: %w", zone, err)
	}
	type standing struct{ at, outcome string }
	latest := map[string]standing{}
	for _, n := range names {
		project, at, ok := strings.Cut(strings.TrimPrefix(n, prefix), "/")
		if !ok || at <= latest[project].at {
			continue
		}
		outcome, err := s.db.Get(ctx, n)
		if err != nil {
			continue
		}
		latest[project] = standing{at: at, outcome: string(outcome)}
	}
	var owner, first string
	for project, st := range latest {
		if st.outcome != outcomeProven {
			continue
		}
		if owner == "" || st.at < first || (st.at == first && project < owner) {
			owner, first = project, st.at
		}
	}
	return owner, owner != "", nil
}

// record writes one outcome, then deletes the pair's older ones: only the
// latest per pair is ever read and no key is written twice, so a delete can
// only remove what is already superseded.
func (s *Service) record(ctx context.Context, zone, project, outcome string) error {
	prefix := domainProvenPrefix + zone + "/" + project + "/"
	key := fmt.Sprintf("%s%020d", prefix, s.now().UnixNano())
	if err := s.db.Put(ctx, key, []byte(outcome)); err != nil {
		return err
	}
	older, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil
	}
	newest := key
	for _, k := range older {
		newest = max(newest, k)
	}
	for _, k := range older {
		if k != newest {
			s.db.Delete(ctx, k)
		}
	}
	return nil
}

// ancestors is fqdn's zones from the top down, ending with fqdn; a bare
// top-level label is no one's to prove.
func ancestors(fqdn string) []string {
	labels := strings.Split(fqdn, ".")
	var out []string
	for i := len(labels) - 2; i >= 0; i-- {
		out = append(out, strings.Join(labels[i:], "."))
	}
	return out
}

// prove is nil when fqdn is project's, and it is asked only when a deploy
// publishes the name or a certificate for it is issued. The topmost zone
// proven above or at fqdn decides: another project's is a refusal, the
// project's own is the record checked, and with none fqdn's own record is.
// Every outcome is recorded, so a record gone is the zone lost.
func (s *Service) prove(ctx context.Context, project, fqdn string) error {
	fqdn = normal(fqdn)
	if _, err := cid.Decode(project); err != nil || fqdn == "" {
		return fmt.Errorf("%w: %s: project %q", ErrUnproven, fqdn, project)
	}
	zone, held := fqdn, false
	for _, z := range ancestors(fqdn) {
		owner, ok, err := s.provenOwner(ctx, z)
		if err != nil {
			return err
		}
		if ok {
			if owner != project {
				return fmt.Errorf("%w: %s: %s is proven to be another project's", ErrUnproven, fqdn, z)
			}
			zone, held = z, true
			break
		}
	}
	keys, err := s.keys(ctx)
	if err != nil {
		return err
	}
	if err := Prove(ctx, s.resolver, keys, project, zone); err != nil {
		if held {
			if rerr := s.record(ctx, zone, project, outcomeLost); rerr != nil {
				return errors.Join(err, rerr)
			}
		}
		return err
	}
	return s.record(ctx, zone, project, outcomeProven)
}

func (h handler) Claim(ctx context.Context, req *connect.Request[cmv2.ClaimRequest]) (*connect.Response[cmv2.DomainClaim], error) {
	c, err := h.s.claim(ctx, req.Msg.GetFqdn(), req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(c), nil
}

func (h handler) Prove(ctx context.Context, req *connect.Request[cmv2.ClaimRequest]) (*connect.Response[cmv2.Empty], error) {
	if err := h.s.prove(ctx, req.Msg.GetProject(), req.Msg.GetFqdn()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&cmv2.Empty{}), nil
}

// Domains issues claims and proves them, against whichever cm nodes answer.
type Domains struct {
	host    host.Host
	members func() []peer.ID
	local   func() *Service

	mu    sync.Mutex
	conns map[peer.ID]*rpc.Client
}

// NewDomains builds the domain client. members and local are as NewClient's.
func NewDomains(h host.Host, members func() []peer.ID, local func() *Service) (*Domains, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	return &Domains{host: h, members: members, local: local, conns: map[peer.ID]*rpc.Client{}}, nil
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
	return cmv2connect.NewDomainServiceClient(conn.HTTP(), conn.URL(), codec.Option()), nil
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

// Prove is nil when fqdn is proven to be project's, as the first cm node
// that answers says.
func (d *Domains) Prove(ctx context.Context, project, fqdn string) error {
	targets := d.targets()
	if len(targets) == 0 {
		return fmt.Errorf("%w: %s: %w", ErrUnproven, fqdn, ErrNoMembers)
	}
	var errs []error
	for _, p := range targets {
		svc, err := d.at(p)
		if err == nil {
			if _, err = svc.Prove(ctx, connect.NewRequest(&cmv2.ClaimRequest{Fqdn: fqdn, Project: project})); err == nil {
				return nil
			}
			var refused *connect.Error
			if errors.As(err, &refused) && refused.Code() == connect.CodeFailedPrecondition {
				return fmt.Errorf("%w%s", ErrUnproven, strings.TrimPrefix(refused.Message(), ErrUnproven.Error()))
			}
		}
		if errors.Is(err, ErrUnproven) {
			return err
		}
		errs = append(errs, fmt.Errorf("%s: %w", p, err))
	}
	return fmt.Errorf("%w: %s: %w", ErrUnproven, fqdn, errors.Join(errs...))
}

// under is name, or a name below zone.
func under(zone, name string) bool {
	return zone != "" && (name == zone || strings.HasSuffix(name, "."+zone))
}
