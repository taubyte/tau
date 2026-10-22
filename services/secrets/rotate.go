package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/cloudflare/circl/group"
	peerCore "github.com/libp2p/go-libp2p/core/peer"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// An epoch is one group key and its membership. Rotating means generating a new
// one and re-encrypting under it -- not resharing the old secret.
//
// Resharing would keep the group secret alive across epochs, so existing
// wrappings would never need rewriting. That is exactly why it is wrong here: a
// single compromise, ever, would be retroactive and total for the life of the
// cluster. Re-encryption costs one threshold decryption per secret, and a
// secrets store holds tens or hundreds of them.
//
// The steps, each a fact derived from stored state rather than a decision
// somebody has to make:
//
//	trigger      the roster changed, or the active key is older than policy
//	generate     a round at the next epoch over the current roster
//	activate     a node offers a key once IT holds a share of it
//	re-encrypt   every secret gains a wrapping under the new epoch
//	retire       a secret's older wrappings go once it has a newer one
func (srv *Service) reconcile(ctx context.Context) error {
	// Repair before anything else. A node that lost its share is not a reason to
	// rotate -- rotating would re-encrypt every secret to work around a node
	// that can simply be handed back what it already had.
	srv.repairShares(ctx)

	if err := srv.rotateIfNeeded(ctx); err != nil {
		return err
	}

	active, err := srv.ActiveKey(ctx)
	if err != nil {
		return err
	}

	if err := srv.reEncrypt(ctx, active); err != nil {
		return err
	}

	return srv.retire(ctx, active)
}

// repairShares rebuilds any share this node should hold but cannot open.
//
// A share can become unopenable -- the at-rest key changed, the data directory
// came back from a backup, the disk corrupted -- and a node in that state is
// silently not a holder: it answers nothing, while the key metadata still counts
// it as a member and advertises a threshold the cluster can no longer reach.
//
// Repair, not rotation. Because dealing is deterministic, the other members can
// replay exactly what they dealt, so the node rebuilds the share it used to
// hold. Nothing is re-encrypted and no epoch moves. Rotating instead would make
// every node redo a threshold decryption for every secret, and would still be
// wrong: the trigger for it can only be "a peer did not answer", which a
// timeout produces just as readily as a real fault.
//
// Best effort by design. A member that is down means one round of replay fails
// and the next pass tries again; nothing else is blocked in the meantime.
func (srv *Service) repairShares(ctx context.Context) {
	keys, err := srv.keys.Newest(ctx)
	if err != nil {
		return
	}

	inUse, err := srv.keysInUse(ctx)
	if err != nil {
		return
	}

	self := srv.self()
	for _, key := range keys {
		if !slices.Contains(key.Members, self) {
			continue
		}
		// Only keys something is still wrapped under. Key material is kept
		// forever, so repairing on membership alone would let a node holding
		// nothing but its private key pull back every share that identity was
		// ever entitled to, including ones the cluster has long moved past. That
		// would make the private key alone as good as the private key plus the
		// share store.
		if _, ok := inUse[key.KeyID]; !ok {
			continue
		}
		if _, err := srv.shares.Get(ctx, key.KeyID); err == nil {
			continue
		}

		if err := srv.recoverShare(ctx, key); err != nil {
			serviceLog.Errorf("rebuilding this node's share of %s: %s", key.KeyID, err)

			continue
		}

		serviceLog.Infof("rebuilt this node's share of %s from its members", key.KeyID)
	}
}

// keysInUse is the set of keys some stored secret is still wrapped under, plus
// the active one.
func (srv *Service) keysInUse(ctx context.Context) (map[string]struct{}, error) {
	inUse := make(map[string]struct{})

	if active, err := srv.ActiveKey(ctx); err == nil {
		inUse[active.KeyID] = struct{}{}
	}

	versions, err := srv.versions(ctx)
	if err != nil {
		return nil, err
	}

	for _, v := range versions {
		keyIDs, err := srv.wrappings.KeyIDs(ctx, v)
		if err != nil {
			continue
		}
		for _, keyID := range keyIDs {
			inUse[keyID] = struct{}{}
		}
	}

	return inUse, nil
}

// versions is every stored version of every secret.
func (srv *Service) versions(ctx context.Context) ([]version, error) {
	refs, err := srv.records.All(ctx)
	if err != nil {
		return nil, err
	}

	var out []version
	for _, ref := range refs {
		versions, err := srv.records.Versions(ctx, ref)
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			out = append(out, v.version)
		}
	}
	return out, nil
}

// rotateIfNeeded starts a new epoch when the roster has changed or the active
// key has aged out. Both triggers are evaluated independently on every node;
// they converge because the next epoch is derived, so two nodes deciding to
// rotate at the same moment join the same round.
func (srv *Service) rotateIfNeeded(ctx context.Context) error {
	active, err := srv.ActiveKey(ctx)
	if err != nil {
		return srv.ensureKey(ctx)
	}

	members, err := srv.roster(ctx)
	if err != nil {
		return err
	}

	aged, err := srv.aged(ctx, active.KeyID)
	if err != nil {
		return err
	}

	if slices.Equal(members, active.Members) && !aged {
		srv.rememberRoster(members)

		return nil
	}

	// Discovery does not hand every node the same roster at the same instant.
	// Acting on the first sighting lets nodes rotate to slightly different
	// rosters, and since each new key raises the epoch the next node counts
	// from, one membership change can cascade into several epochs -- each one a
	// DKG and a full re-encryption. So a roster has to survive two consecutive
	// passes before it is worth rotating for. Local, no clock, and the
	// reconcile interval supplies the spacing.
	if !srv.rememberRoster(members) {
		return nil
	}

	// Somebody may have rotated to this roster already. Without this check each
	// node counts from the epoch the previous one just published and rotates
	// again -- one membership change cascading into an epoch per node, each a
	// DKG and a full re-encryption.
	//
	// Both the store and the members are consulted. The store is cheap but
	// lags, and under replication delay a stale "no key yet" is exactly what
	// starts the cascade, so the members are asked directly as well.
	keys, err := srv.keys.Newest(ctx)
	if err != nil {
		return err
	}
	var epoch uint64
	for _, key := range keys {
		if key.Epoch > active.Epoch && slices.Equal(key.Members, members) {
			return nil
		}
		epoch = max(epoch, key.Epoch)
	}
	if srv.alreadyRotated(members, active.Epoch) {
		return nil
	}

	// The next epoch comes from the same read that just found no rotation, not
	// a fresh one. A key for this roster can land between two reads, and a
	// second read that sees it counts past it: the check said "not rotated"
	// and the count said "rotated", so this node rotated again on top of a
	// rotation that had already happened. From one read, a stale view only
	// names the round already under way, and joinRound is idempotent, so this
	// converges rather than competing.
	return srv.generateKey(ctx, epoch+1, members)
}

// rememberRoster records the roster this pass saw and reports whether the
// previous pass saw the same one.
func (srv *Service) rememberRoster(members []string) bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	seenBefore := slices.Equal(srv.lastRoster, members)
	srv.lastRoster = slices.Clone(members)

	return seenBefore
}

// aged reports whether the active key has outlived the rotation policy.
//
// Age is measured from when THIS node stored its share, not from a timestamp in
// the shared record: a wall clock in a record every member writes would be a
// value two nodes disagree on. Nodes therefore age a key out at slightly
// different moments, which costs nothing -- whoever goes first defines the
// round the others join.
func (srv *Service) aged(ctx context.Context, keyID string) (bool, error) {
	if srv.rotateAfter <= 0 {
		return false, nil
	}

	stored, err := srv.shares.StoredAt(ctx, keyID)
	if err != nil {
		return false, nil // no share here, so not this node's call to make
	}

	return srv.now().Sub(stored) >= srv.rotateAfter, nil
}

// reEncrypt gives every version a wrapping under the active key.
//
// Only the data key is re-wrapped. The ciphertext is untouched, so this never
// needs the plaintext -- but it does need the data key, which means a threshold
// decryption per version and a moment where this node holds that key in memory.
// That is the cost of regenerating rather than resharing.
func (srv *Service) reEncrypt(ctx context.Context, active *KeyMeta) error {
	versions, err := srv.versions(ctx)
	if err != nil {
		return err
	}

	public, err := active.PublicKey()
	if err != nil {
		return err
	}

	var failures []error
	for _, v := range versions {
		if _, _, err := srv.wrappings.Get(ctx, v, active.KeyID); err == nil {
			continue // already carried forward
		}

		if err := srv.carryForward(ctx, v, active, public); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", v, err))
		}
	}

	return errors.Join(failures...)
}

// carryForward re-wraps one version's data key under the active key.
func (srv *Service) carryForward(ctx context.Context, v version, active *KeyMeta, public group.Element) error {
	from, wrapping, writer, err := srv.readable(ctx, v)
	if err != nil {
		return err
	}

	dek, err := srv.recoverDEK(ctx, v, from, wrapping, writer)
	if err != nil {
		return err
	}

	rewrapped, err := tdh.Wrap(public, dek, wrapAAD(v, active.KeyID))
	if err != nil {
		return err
	}

	return srv.wrappings.Put(ctx, v, active.KeyID, srv.self(), rewrapped)
}

// retire drops wrappings this cluster no longer needs.
//
// There is no vote and nothing to agree on. Each deletion is justified by a fact
// about the one secret it touches, and by evidence this node gathered itself.
//
// The evidence is the point. Carrying a secret forward proves the OLD wrapping
// could be opened; it proves nothing about the new one. A new wrapping exists as
// soon as one node writes it, but it is only *readable* once enough members
// durably hold a share of the key it was made under -- and a node cannot know
// that by looking. So before deleting the last readable copy, this node opens
// the new wrapping for real. If the threshold cannot be reached, the new
// wrapping is not yet a replacement and the old one stays.
//
// Without that check, deleting on existence alone destroys the secret whenever
// fewer than t members persisted a share of the new key: the replacement is
// unopenable and the readable original is gone.
//
// It deliberately never deletes key material. A share is 32 bytes and a key
// record is barely more, while deciding that nobody anywhere still needs them
// would take exactly the cluster-wide agreement this design does not have. So
// wrappings -- the part that grows with the number of secrets -- are collected,
// and the per-epoch material is kept.
func (srv *Service) retire(ctx context.Context, active *KeyMeta) error {
	versions, err := srv.versions(ctx)
	if err != nil {
		return err
	}

	var failures []error
	for _, v := range versions {
		superseded, err := srv.supersededKeys(ctx, v, active)
		if err != nil {
			failures = append(failures, err)
			continue
		}

		for _, keyID := range superseded {
			if err := srv.wrappings.DeleteKey(ctx, v, keyID); err != nil {
				failures = append(failures, err)
			}
		}
	}

	if err := errors.Join(failures...); err != nil {
		return err
	}

	// Deletion writes tombstones, so the store has to be compacted for the
	// space to come back. Compact documents itself as coordination-free, so
	// every node may run it.
	return srv.compact(ctx)
}

// supersededKeys lists the keys whose wrappings of this version are safe to
// drop: only once the active key's wrapping has been proven openable.
func (srv *Service) supersededKeys(ctx context.Context, v version, active *KeyMeta) ([]string, error) {
	keyIDs, err := srv.wrappings.KeyIDs(ctx, v)
	if err != nil {
		return nil, err
	}
	if len(keyIDs) <= 1 {
		// Nothing is superseded, so there is nothing to prove. Without this the
		// proof below runs a threshold decryption for every secret on every
		// pass forever, long after the last old wrapping went.
		return nil, nil
	}
	if !slices.Contains(keyIDs, active.KeyID) {
		return nil, nil // not carried forward yet, so nothing here is redundant
	}

	replacement, writer, err := srv.wrappings.Get(ctx, v, active.KeyID)
	if err != nil {
		return nil, nil
	}

	// The load-bearing line: open it, do not assume it opens.
	if _, err := srv.recoverDEK(ctx, v, active, replacement, writer); err != nil {
		return nil, nil
	}

	// Strictly older, by epoch -- never "everything that is not the key I chose".
	//
	// Two ways that bites. A node whose share of a NEWER key is broken falls
	// back to serving an older one, and would then delete the newer key's
	// wrappings that everyone else can still open. And after a partition heals,
	// two keys can sit at the SAME epoch: each side proves its own opens and
	// deletes the other's, so both tombstones survive and both valid wrappings
	// are gone. Same-epoch competitors must outlive each other until a later
	// epoch supersedes them both.
	superseded := make([]string, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		if keyID == active.KeyID {
			continue
		}

		other, err := srv.keys.Get(ctx, keyID)
		if err != nil {
			continue // unknown key: not ours to judge, let alone delete
		}
		if other.Epoch >= active.Epoch {
			continue
		}

		superseded = append(superseded, keyID)
	}

	return superseded, nil
}

// recoverDEK opens a secret's data key, using this node's own share plus
// partials collected from the rest of the key's members. The plaintext is never
// involved: only the wrapped data key is recovered, and only to re-wrap it.
func (srv *Service) recoverDEK(ctx context.Context, v version, key *KeyMeta, wrapping *tdh.Wrapping, writer string) ([]byte, error) {
	public, err := key.PublicKey()
	if err != nil {
		return nil, err
	}

	partials := make([]*tdh.Partial, 0, key.Threshold)
	if share, err := srv.shares.Get(ctx, key.KeyID); err == nil {
		if partial, err := tdh.PartialDecrypt(share, wrapping); err == nil {
			partials = append(partials, partial)
		}
	}

	var failures []error
	for _, member := range key.Members {
		if uint(len(partials)) == key.Threshold {
			break
		}
		if member == srv.self() {
			continue
		}

		partial, err := srv.askPartial(member, v, key.KeyID, writer)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", member, err))
			continue
		}
		partials = append(partials, partial)
	}

	if uint(len(partials)) < key.Threshold {
		return nil, fmt.Errorf("collected %d of %d partials for %s: %w",
			len(partials), key.Threshold, v, errors.Join(failures...))
	}

	return tdh.Unwrap(wrapping, public, partials, key.Threshold, wrapAAD(v, key.KeyID))
}

func (srv *Service) askPartial(member string, v version, keyID, writer string) (*tdh.Partial, error) {
	to, err := peerCore.Decode(member)
	if err != nil {
		return nil, err
	}

	api, err := srv.secretsAt(to)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(srv.ctx, callTimeout)
	defer cancel()
	resp, err := api.Partial(ctx, connect.NewRequest(&secretsv2.PartialRequest{Ref: wireRef(v.Ref), Cid: v.CID, KeyId: keyID, Writer: writer}))
	if err != nil {
		return nil, err
	}

	return &tdh.Partial{ID: resp.Msg.GetId(), D: resp.Msg.GetD()}, nil
}

// compact reclaims the space behind the tombstones deletion leaves. Compact
// documents itself as coordination-free, so every node may run it.
func (srv *Service) compact(ctx context.Context) error {
	compactor, ok := srv.db.(interface {
		Compact(context.Context, string) (int, error)
	})
	if !ok {
		return nil
	}

	if _, err := compactor.Compact(ctx, ""); err != nil {
		return fmt.Errorf("compacting after retiring an epoch: %w", err)
	}

	return nil
}
