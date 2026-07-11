package hoarder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	mh "github.com/taubyte/tau/utils/multihash"
)

// RegistryMeta is the durable placement record for one database/storage
// instance, written once at placement and deleted on config removal. It carries
// identity only — the replica target is system-calculated, never stored.
type RegistryMeta struct {
	Kind          hoarderIface.ResourceKind
	ConfigId      string
	ProjectId     string
	ApplicationId string
	Match         string
	Branch        string
}

// Claim records that a peer holds a resource. Value is minimal (timestamp) so
// re-claims are no-ops — the shared CRDT kvdb must only grow on real change.
type Claim struct {
	Since int64
}

// StashMeta is the durable placement record for a stashed CID.
type StashMeta struct {
	Target    int
	OwnerHash string
}

// instanceHash is the logical instance ID: mh.Hash(project+app+match). Identical
// to substrate's kvdb data path (services/substrate/components/*/common/hash.go)
// and to the DHT discovery namespace — one string names the CRDT, the registry
// entry, and the rendezvous.
func instanceHash(m hoarderIface.MetaData) string {
	return mh.Hash(m.ProjectId + m.ApplicationId + m.Match)
}

// --- resource meta ---

// putMeta writes the placement record only if absent or changed (write-on-change).
func (srv *Service) putMeta(ctx context.Context, hash string, meta *RegistryMeta) error {
	b, err := cbor.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal meta failed with: %w", err)
	}
	if cur, _ := srv.db.Get(ctx, hoarderSpecs.MetaKey(hash)); cur != nil && string(cur) == string(b) {
		return nil
	}
	return srv.db.Put(ctx, hoarderSpecs.MetaKey(hash), b)
}

func (srv *Service) getMeta(ctx context.Context, hash string) (*RegistryMeta, error) {
	b, err := srv.db.Get(ctx, hoarderSpecs.MetaKey(hash))
	if err != nil {
		return nil, err
	}
	meta := new(RegistryMeta)
	if err := cbor.Unmarshal(b, meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta failed with: %w", err)
	}
	return meta, nil
}

func (srv *Service) deleteMeta(ctx context.Context, hash string) error {
	return srv.db.Delete(ctx, hoarderSpecs.MetaKey(hash))
}

// listMetaHashes returns every instance hash with a placement record.
func (srv *Service) listMetaHashes(ctx context.Context) ([]string, error) {
	keys, err := srv.db.List(ctx, hoarderSpecs.MetaPrefix)
	if err != nil {
		return nil, err
	}
	return trimPrefixEach(keys, hoarderSpecs.MetaPrefix), nil
}

// --- claims ---

// addClaim records this-or-another peer as a holder. No-op if already claimed
// (keeps the CRDT from growing on repeated claims of the same holder).
func (srv *Service) addClaim(ctx context.Context, hash, peerID string) error {
	key := hoarderSpecs.ClaimKey(hash, peerID)
	if cur, _ := srv.db.Get(ctx, key); cur != nil {
		return nil
	}
	b, err := cbor.Marshal(&Claim{Since: time.Now().Unix()})
	if err != nil {
		return fmt.Errorf("marshal claim failed with: %w", err)
	}
	return srv.db.Put(ctx, key, b)
}

func (srv *Service) releaseClaim(ctx context.Context, hash, peerID string) error {
	return srv.db.Delete(ctx, hoarderSpecs.ClaimKey(hash, peerID))
}

// claimSince returns when peerID claimed hash (unix seconds), or ok=false if
// there is no such claim.
func (srv *Service) claimSince(ctx context.Context, hash, peerID string) (int64, bool) {
	b, err := srv.db.Get(ctx, hoarderSpecs.ClaimKey(hash, peerID))
	if err != nil || b == nil {
		return 0, false
	}
	c := new(Claim)
	if cbor.Unmarshal(b, c) != nil {
		return 0, false
	}
	return c.Since, true
}

// listClaims returns the peer IDs claiming a resource (registry truth, which
// may include dead peers — cross with FindPeers for liveness).
func (srv *Service) listClaims(ctx context.Context, hash string) ([]string, error) {
	keys, err := srv.db.List(ctx, hoarderSpecs.ClaimsPathOf(hash))
	if err != nil {
		return nil, err
	}
	return lastSegmentEach(keys), nil
}

// --- stash ---

func (srv *Service) putStashMeta(ctx context.Context, cid string, meta *StashMeta) error {
	b, err := cbor.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal stash meta failed with: %w", err)
	}
	if cur, _ := srv.db.Get(ctx, hoarderSpecs.StashMetaKey(cid)); cur != nil && string(cur) == string(b) {
		return nil
	}
	return srv.db.Put(ctx, hoarderSpecs.StashMetaKey(cid), b)
}

func (srv *Service) getStashMeta(ctx context.Context, cid string) (*StashMeta, error) {
	b, err := srv.db.Get(ctx, hoarderSpecs.StashMetaKey(cid))
	if err != nil {
		return nil, err
	}
	m := new(StashMeta)
	if err := cbor.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("unmarshal stash meta failed with: %w", err)
	}
	return m, nil
}

func (srv *Service) addStashClaim(ctx context.Context, cid, peerID string) error {
	key := hoarderSpecs.StashClaimKey(cid, peerID)
	if cur, _ := srv.db.Get(ctx, key); cur != nil {
		return nil
	}
	b, err := cbor.Marshal(&Claim{Since: time.Now().Unix()})
	if err != nil {
		return fmt.Errorf("marshal stash claim failed with: %w", err)
	}
	return srv.db.Put(ctx, key, b)
}

// stashClaimSince returns when peerID claimed the stashed cid, or ok=false.
func (srv *Service) stashClaimSince(ctx context.Context, cid, peerID string) (int64, bool) {
	b, err := srv.db.Get(ctx, hoarderSpecs.StashClaimKey(cid, peerID))
	if err != nil || b == nil {
		return 0, false
	}
	c := new(Claim)
	if cbor.Unmarshal(b, c) != nil {
		return 0, false
	}
	return c.Since, true
}

func (srv *Service) listStashClaims(ctx context.Context, cid string) ([]string, error) {
	keys, err := srv.db.List(ctx, hoarderSpecs.StashClaimsPathOf(cid))
	if err != nil {
		return nil, err
	}
	return lastSegmentEach(keys), nil
}

// listStashCids returns every CID with a stash placement record.
func (srv *Service) listStashCids(ctx context.Context) ([]string, error) {
	keys, err := srv.db.List(ctx, hoarderSpecs.StashMetaPrefix)
	if err != nil {
		return nil, err
	}
	return trimPrefixEach(keys, hoarderSpecs.StashMetaPrefix), nil
}

// targetReplicas is the system-calculated replica target: the desired default
// clamped to the live fleet, so single-node clouds converge to 1.
func targetReplicas(liveFleet int) int {
	return max(min(hoarderSpecs.DefaultReplicaTarget, liveFleet), 1)
}

func trimPrefixEach(keys []string, prefix string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, strings.TrimPrefix(k, prefix))
	}
	return out
}

func lastSegmentEach(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		seg := strings.Split(strings.TrimRight(k, "/"), "/")
		out = append(out, seg[len(seg)-1])
	}
	return out
}
