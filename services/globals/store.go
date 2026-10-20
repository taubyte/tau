package globals

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/pkg/certificate/acme"
	globalsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/globals/v2"
)

// store.go is the keys this service writes. Each one is written by one node
// and never by two with different content (AGENTS.md, "Designing around
// kvdb"): an issuance carries its own id, a lease its holder, a challenge the
// digest of its value.

const (
	certPrefix  = "/cert/"
	leasePrefix = "/lease/"
)

// kept is one issuance as stored, with its leaf parsed.
type kept struct {
	record *globalsv2.Issued
	leaf   *x509.Certificate
}

func certKey(name string, notAfter time.Time, id string) string {
	return fmt.Sprintf("%s%s/%020d/%s", certPrefix, name, notAfter.Unix(), id)
}

// newest is the issuance of name expiring last.
func (s *Service) newest(ctx context.Context, name string) (*kept, error) {
	keys, err := s.db.List(ctx, certPrefix+name+"/")
	if err != nil {
		return nil, fmt.Errorf("globals: listing %s: %w", name, err)
	}
	slices.Sort(keys)
	for i := len(keys) - 1; i >= 0; i-- {
		blob, err := s.db.Get(ctx, keys[i])
		if err != nil {
			continue
		}
		var rec globalsv2.Issued
		if proto.Unmarshal(blob, &rec) != nil {
			continue
		}
		block, _ := pem.Decode(rec.Chain)
		if block == nil {
			continue
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !s.now().Before(leaf.NotAfter) {
			continue
		}
		return &kept{record: &rec, leaf: leaf}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotKept, name)
}

// keptNames is every name something is kept for.
func (s *Service) keptNames(ctx context.Context) ([]string, error) {
	keys, err := s.db.List(ctx, certPrefix)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		name, _, _ := strings.Cut(strings.TrimPrefix(k, certPrefix), "/")
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// store is the issuer's Store over this service's database and the vault.
type store struct{ s *Service }

// Load is the newest issuance's chain and key, as one blob.
func (st store) Load(ctx context.Context, name string) ([]byte, error) {
	k, err := st.s.newest(ctx, name)
	if errors.Is(err, ErrNotKept) {
		return nil, acme.ErrMiss
	}
	if err != nil {
		return nil, err
	}
	key, err := st.s.vault.Get(ctx, k.record.Key.Project, k.record.Key.Environment, k.record.Key.Name)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), k.record.Chain...), key...), nil
}

// Keep writes the key to the vault first and the chain after, so a chain is
// never listed whose key is not kept.
func (st store) Keep(ctx context.Context, name string, blob []byte) error {
	s := st.s
	if s.vault == nil {
		return ErrNoVault
	}
	var chain, key []byte
	var leaf *x509.Certificate
	for rest := blob; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			if leaf == nil {
				parsed, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return err
				}
				leaf = parsed
			}
			chain = append(chain, pem.EncodeToMemory(block)...)
		case strings.HasSuffix(block.Type, "PRIVATE KEY"):
			key = pem.EncodeToMemory(block)
		}
	}
	if leaf == nil || key == nil {
		return errors.New("globals: an issuance holds no chain or no key")
	}

	id := issuanceID()
	ref := &globalsv2.SecretRef{Project: VaultProject, Environment: VaultEnvironment, Name: "leaf-" + id}
	if err := s.vault.Set(ctx, ref.Project, ref.Environment, ref.Name, key); err != nil {
		return fmt.Errorf("globals: keeping the key of %s: %w", name, err)
	}
	rec, err := proto.Marshal(&globalsv2.Issued{Name: name, Chain: chain, Key: ref, NotAfter: leaf.NotAfter.Unix()})
	if err != nil {
		return err
	}
	return s.db.Put(ctx, certKey(name, leaf.NotAfter, id), rec)
}

func issuanceID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// claim writes this node's lease on name and reports whether it is the one
// that orders: of the live leases, the earliest, then the lowest peer id.
func (s *Service) claim(ctx context.Context, name string) (bool, error) {
	now := s.now()
	// THE ENCODED ID: a peer.ID is raw multihash bytes, which no key may
	// carry.
	self := s.host.ID().String()
	value := strconv.FormatInt(now.UnixNano(), 10) + " " + strconv.FormatInt(now.Add(s.cfg.Lease).UnixNano(), 10)
	if err := s.db.Put(ctx, leasePrefix+name+"/"+self, []byte(value)); err != nil {
		return false, fmt.Errorf("globals: leasing %s: %w", name, err)
	}
	keys, err := s.db.List(ctx, leasePrefix+name+"/")
	if err != nil {
		return false, fmt.Errorf("globals: reading the leases on %s: %w", name, err)
	}
	var winner string
	var winnerSince int64
	for _, k := range keys {
		holder := k[strings.LastIndex(k, "/")+1:]
		blob, err := s.db.Get(ctx, k)
		if err != nil {
			continue
		}
		since, expires, ok := parseLease(string(blob))
		if !ok || expires <= now.UnixNano() {
			continue
		}
		if winner == "" || since < winnerSince || (since == winnerSince && holder < winner) {
			winner, winnerSince = holder, since
		}
	}
	return winner == self, nil
}

func parseLease(v string) (since, expires int64, ok bool) {
	a, b, found := strings.Cut(v, " ")
	if !found {
		return 0, 0, false
	}
	since, err1 := strconv.ParseInt(a, 10, 64)
	expires, err2 := strconv.ParseInt(b, 10, 64)
	return since, expires, err1 == nil && err2 == nil
}

// release withdraws this node's lease on name.
func (s *Service) release(name string) {
	if err := s.db.Delete(context.Background(), leasePrefix+name+"/"+s.host.ID().String()); err != nil {
		s.logf("globals: releasing the lease on %s: %v", name, err)
	}
}

// Challenges are the live DNS-01 values for fqdn.
func (s *Service) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	if s.challenges == nil {
		return nil, nil
	}
	return s.challenges.Challenges(ctx, fqdn)
}

// Thumbprint is the account key's thumbprint.
func (s *Service) Thumbprint(ctx context.Context) (string, error) {
	issuer, err := s.acmeIssuer(ctx)
	if err != nil {
		return "", err
	}
	return issuer.Thumbprint()
}
