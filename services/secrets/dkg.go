package secrets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/cloudflare/circl/secretsharing"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	peerCore "github.com/libp2p/go-libp2p/core/peer"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

const (
	maxMembers   = 10
	maxRounds    = 8
	roundTimeout = 60 * time.Second
)

// round is one DKG this node takes part in. There is no coordinator: a round is
// named by its roster, so two nodes that start one concurrently start the same
// round and their contributions merge instead of racing to produce two keys.
type round struct {
	id        string
	epoch     uint64
	members   []string // peer ids as strings, sorted
	threshold uint
	created   time.Time

	dealer *tdh.Dealer
	deal   sync.Once
	key    *tdh.KeySet
	done   chan struct{}
}

// roundID names a round by its epoch and roster. Deriving it rather than
// drawing it makes concurrent starts converge: same epoch, same membership,
// same round.
//
// The epoch is what lets an unchanged roster rotate. Without it the round id
// would be a pure function of membership, the dealer seeds would be unchanged,
// and re-running would reproduce the same group key forever.
func roundID(epoch uint64, members []string) string {
	h := sha256.New()
	h.Write([]byte("tau/secrets/round/v2"))
	binary.Write(h, binary.BigEndian, epoch)
	h.Write([]byte(strings.Join(members, ",")))

	return hex.EncodeToString(h.Sum(nil))
}

// thresholdFor is a majority of the roster, capped so the cluster can always
// lose one node and still reach it. It is derived, never taken from a caller: a
// threshold that arrives over the wire is one an attacker can set to 1.
//
// The cap is what makes node loss survivable without anybody being told. A
// majority alone gives t=2 at two nodes, where losing either one leaves a
// survivor holding one share and needing two: it can no longer recover any data
// key, so every secret is gone and only a human with a backup can help. Nothing
// announces a departure -- a removed node simply stops answering -- so the only
// way to survive it is to never be in a state that cannot.
//
// The cost is honest and confined to n=2: t=1 there means a single compromised
// node reads everything. Two nodes cannot have both properties, and the
// threshold guarantee was already emergent at three or more; availability is
// the one an unattended system cannot do without.
func thresholdFor(members []string) uint {
	majority := len(members)/2 + 1
	if survivable := len(members) - 1; survivable > 0 && majority > survivable {
		return uint(survivable)
	}

	return uint(majority)
}

// ensureKey generates a distributed key if the cluster has none.
//
// "None" is the hard part. A node that has just joined cannot tell an empty
// cluster from a store it has not replicated yet, and if it guesses wrong it
// creates a key only it holds -- then keeps offering that key, because
// ActiveKey answers with the newest key this node has a share of. Clients
// reaching it would get secrets wrapped at threshold 1 on a single machine
// until the next rotation healed it.
//
// So it asks. Peers are the authority on whether a key exists, and asking is a
// question, not a vote: one answer is enough to stop, and no answer from
// anybody is the only thing that justifies creating one.
func (srv *Service) ensureKey(ctx context.Context) error {
	if _, err := srv.ActiveKey(ctx); err == nil {
		return nil
	}

	members, err := srv.roster(ctx)
	if err != nil {
		return err
	}

	if holder, epoch, roster, found := srv.peerWithKey(members); found {
		// A ROUND THIS NODE IS IN CAN FINISH ELSEWHERE FIRST. Nodes that
		// bootstrap together join each other's round through a contribution
		// before their own ensureKey gets this far, so a peer that already
		// completed is very often completing the round this node is part of.
		// That is not a key to wait to replicate -- it is this node's own
		// round, one contribution from done -- and calling it an error made a
		// concurrent bootstrap fail whenever one member finished a moment
		// before another asked. Wait for it instead.
		if r, joined := srv.joined(roundID(epoch, roster)); joined {
			return srv.await(ctx, r)
		}
		return fmt.Errorf("%s already has a key; waiting to replicate it", holder)
	}

	epoch, err := srv.keys.Epoch(ctx)
	if err != nil {
		return err
	}

	return srv.generateKey(ctx, epoch, members)
}

// peerWithKey reports whether any discovered member already serves a key.
func (srv *Service) peerWithKey(members []string) (string, uint64, []string, bool) {
	self := srv.self()

	for _, member := range members {
		if member == self {
			continue
		}

		if epoch, roster, ok := srv.peerKey(member); ok {
			return member, epoch, roster, true
		}
	}

	return "", 0, nil, false
}

// joined is the round this node is already taking part in under id, if any.
func (srv *Service) joined(id string) (*round, bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	r, ok := srv.rounds[id]
	return r, ok
}

// await waits for a round to complete.
func (srv *Service) await(ctx context.Context, r *round) error {
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(roundTimeout):
		return fmt.Errorf("round %s did not complete", r.id)
	}
}

// peerKey asks one member what key it is serving. Asking beats reading the
// shared store when the question is "has this already happened": a store that
// has not caught up answers no, and acting on that no is what turns one
// membership change into an epoch per node.
func (srv *Service) peerKey(member string) (epoch uint64, roster []string, ok bool) {
	to, err := peerCore.Decode(member)
	if err != nil {
		return 0, nil, false
	}

	ctx, cancel := context.WithTimeout(srv.ctx, callTimeout)
	defer cancel()

	api, err := srv.secretsAt(to)
	if err != nil {
		return 0, nil, false
	}
	resp, err := api.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{}))
	if err != nil || resp.Msg.GetKeyId() == "" {
		return 0, nil, false // no key, or cannot answer; neither is an answer of yes
	}

	return resp.Msg.GetEpoch(), resp.Msg.GetMembers(), true
}

// alreadyRotated reports whether some member is already serving a key for this
// roster at a newer epoch, in which case the rotation this node wants has
// happened and starting another only burns an epoch.
func (srv *Service) alreadyRotated(members []string, since uint64) bool {
	self := srv.self()

	for _, member := range members {
		if member == self {
			continue
		}

		epoch, roster, ok := srv.peerKey(member)
		if ok && epoch > since && slices.Equal(roster, members) {
			return true
		}
	}

	return false
}

// generateKey runs one round at the given epoch over the given roster, and
// waits for it so the caller learns whether it worked.
//
// The roster is a parameter, not something this rediscovers. A caller has
// usually just decided the roster is worth acting on -- rotateIfNeeded only
// proceeds once one has survived two consecutive passes -- and discovering it
// again here would throw that away and generate for whatever the last poll
// happened to see.
func (srv *Service) generateKey(ctx context.Context, epoch uint64, members []string) error {
	r, err := srv.joinRound(ctx, roundID(epoch, members), epoch, members)
	if err != nil {
		return err
	}

	srv.dealOnce(r)

	return srv.await(ctx, r)
}

// joinRound returns this node's round, creating it if this is the first time
// the node hears of it. A round a node did not start is as valid as one it did.
func (srv *Service) joinRound(ctx context.Context, id string, epoch uint64, members []string) (*round, error) {
	if id != roundID(epoch, members) {
		return nil, fmt.Errorf("round %s does not name its epoch and roster", id)
	}

	threshold := thresholdFor(members)

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if r, ok := srv.rounds[id]; ok {
		return r, nil
	}

	seed, err := srv.dealerSeed(id)
	if err != nil {
		return nil, err
	}

	dealer, err := tdh.NewDealer(srv.self(), members, threshold, seed)
	if err != nil {
		return nil, fmt.Errorf("joining round %s: %w", id, err)
	}

	srv.evictOldestLocked()
	r := &round{
		id:        id,
		epoch:     epoch,
		members:   members,
		threshold: threshold,
		created:   srv.now(),
		dealer:    dealer,
		done:      make(chan struct{}),
	}
	srv.rounds[id] = r

	// A one-node roster is complete the moment it is created.
	if err := srv.completeLocked(ctx, r); err != nil {
		return nil, err
	}

	return r, nil
}

// evictOldestLocked keeps the round table bounded. Evicting a finished round
// costs nothing -- its share is already on disk.
func (srv *Service) evictOldestLocked() {
	for len(srv.rounds) >= maxRounds {
		var oldest *round
		for _, r := range srv.rounds {
			if oldest == nil || r.created.Before(oldest.created) {
				oldest = r
			}
		}
		delete(srv.rounds, oldest.id)
	}
}

// dealerSeed derives this node's polynomial for one round from its identity, so
// dealing is idempotent across restarts. It must never leave the node.
func (srv *Service) dealerSeed(id string) ([]byte, error) {
	priv := srv.host.Peerstore().PrivKey(srv.host.ID())
	if priv == nil {
		return nil, errors.New("node has no private key to derive a contribution from")
	}

	raw, err := libp2pCrypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling node private key: %w", err)
	}

	seed := sha256.New()
	seed.Write(raw)
	seed.Write([]byte(id))

	return seed.Sum(nil), nil
}

// dealOnce sends this node's contribution to every other member, at most once
// per round however many times it is called.
func (srv *Service) dealOnce(r *round) {
	r.deal.Do(func() {
		self := srv.self()

		for _, member := range r.members {
			if member == self {
				continue // a dealer's own contribution never goes over the wire
			}

			// Shutdown waits for this, and a member that has gone away costs a
			// full send timeout, so stop at the first sign of closing rather
			// than holding the process open one dead peer at a time.
			if srv.ctx.Err() != nil {
				return
			}

			if err := srv.contribute(r, member); err != nil {
				serviceLog.Errorf("contributing to %s in round %s: %s", member, r.id, err)
			}
		}
	})
}

func (srv *Service) contribute(r *round, member string) error {
	to, err := peerCore.Decode(member)
	if err != nil {
		return fmt.Errorf("decoding member: %w", err)
	}

	share, err := r.dealer.ShareFor(member)
	if err != nil {
		return err
	}

	payload, err := contribution(srv.self(), r.dealer.Commitment(), share)
	if err != nil {
		return err
	}
	payload.Round, payload.Epoch, payload.Members = r.id, r.epoch, r.members

	api, err := srv.dealerAt(to)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(srv.ctx, callTimeout)
	defer cancel()
	_, err = api.Contribute(ctx, connect.NewRequest(payload))

	return err
}

// contribution serializes one dealer's commitment and the share it owes one
// member.
func contribution(from string, commitment secretsharing.SecretCommitment, share secretsharing.Share) (*secretsv2.Contribution, error) {
	out := &secretsv2.Contribution{From: from, Commitment: make([][]byte, len(commitment))}

	var err error
	for i, c := range commitment {
		if out.Commitment[i], err = c.MarshalBinaryCompress(); err != nil {
			return nil, fmt.Errorf("marshaling commitment coefficient %d: %w", i, err)
		}
	}
	if out.ShareId, err = share.ID.MarshalBinary(); err != nil {
		return nil, fmt.Errorf("marshaling share id: %w", err)
	}
	if out.ShareValue, err = share.Value.MarshalBinary(); err != nil {
		return nil, fmt.Errorf("marshaling share value: %w", err)
	}

	return out, nil
}

// contributed deserializes what contribution serialized.
func contributed(p *secretsv2.Contribution) (secretsharing.SecretCommitment, secretsharing.Share, error) {
	var share secretsharing.Share

	commitment := make(secretsharing.SecretCommitment, len(p.GetCommitment()))
	for i, b := range p.GetCommitment() {
		e := tdh.Group.NewElement()
		if err := e.UnmarshalBinary(b); err != nil {
			return nil, share, fmt.Errorf("commitment coefficient %d: %w", i, err)
		}
		commitment[i] = e
	}

	share.ID = tdh.Group.NewScalar()
	if err := share.ID.UnmarshalBinary(p.GetShareId()); err != nil {
		return nil, share, fmt.Errorf("share id: %w", err)
	}
	share.Value = tdh.Group.NewScalar()
	if err := share.Value.UnmarshalBinary(p.GetShareValue()); err != nil {
		return nil, share, fmt.Errorf("share value: %w", err)
	}

	return commitment, share, nil
}

// receive accepts one member's contribution, joining the round if this node had
// not heard of it, and finalizes once every contribution is in.
func (srv *Service) receive(ctx context.Context, p *secretsv2.Contribution) error {
	commitment, share, err := contributed(p)
	if err != nil {
		return err
	}

	r, err := srv.joinRound(ctx, p.GetRound(), p.GetEpoch(), p.GetMembers())
	if err != nil {
		return err
	}

	// Deal in the background: this node owes the other members its own
	// contribution, but the sender is waiting on this handler to return.
	srv.background(func() { srv.dealOnce(r) })

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if err := r.dealer.Receive(p.GetFrom(), commitment, share); err != nil {
		return err
	}

	return srv.completeLocked(ctx, r)
}

// completeLocked finalizes a round whose contributions are all in: it persists
// this node's share, then publishes the public half.
//
// Every member publishes, and every member writes the same bytes -- the key id
// is derived from the roster and the group key is the sum of the same
// commitments -- so the record has no single writer to elect and no value two
// nodes could disagree on.
func (srv *Service) completeLocked(ctx context.Context, r *round) error {
	if r.key != nil || !r.dealer.Complete() {
		return nil
	}

	key, err := r.dealer.Finalize()
	if err != nil {
		return fmt.Errorf("finalizing round %s: %w", r.id, err)
	}

	if err := srv.shares.Put(ctx, r.id, key.Share); err != nil {
		return fmt.Errorf("storing share for round %s: %w", r.id, err)
	}

	public, err := key.Public.MarshalBinaryCompress()
	if err != nil {
		return fmt.Errorf("marshaling group public key: %w", err)
	}

	verification := make([][]byte, len(key.Verification))
	for i, v := range key.Verification {
		if verification[i], err = v.MarshalBinaryCompress(); err != nil {
			return fmt.Errorf("marshaling verification coefficient %d: %w", i, err)
		}
	}

	if err := srv.keys.Put(ctx, &KeyMeta{
		KeyID:        r.id,
		Epoch:        r.epoch,
		Public:       public,
		Verification: verification,
		Threshold:    r.threshold,
		Members:      r.members,
	}); err != nil {
		return fmt.Errorf("publishing key %s: %w", r.id, err)
	}

	r.key = key
	close(r.done)

	// Tell the others rather than leaving them to notice on their next tick.
	epoch := r.epoch
	srv.background(func() { srv.announceEpoch(context.WithoutCancel(ctx), epoch) })

	return nil
}

// replay regenerates the contribution this node dealt to `to` in a finished
// round, so a member that lost its share can rebuild it.
//
// It works because dealing is deterministic: the polynomial comes from this
// node's private key and the round id (dealerSeed), so the contribution is the
// same one it sent originally, and the recipient's Feldman check still passes.
//
// `to` is the CALLER's own identity, taken from the authenticated connection
// and never from the request body. A contribution is the share destined for one
// member; handing it to anybody who names a member would let a caller collect
// t of them and reconstruct the group secret outright, which is a far cheaper
// attack than asking t nodes to decrypt one secret at a time.
func (srv *Service) replay(ctx context.Context, keyID, to string) (*secretsv2.Contribution, error) {
	key, err := srv.keys.Get(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(key.Members, to) {
		return nil, fmt.Errorf("%s is not a member of key %s", to, keyID)
	}

	self := srv.self()
	if !slices.Contains(key.Members, self) {
		return nil, fmt.Errorf("this node is not a member of key %s", keyID)
	}
	if to == self {
		return nil, errors.New("a dealer's own contribution never goes over the wire")
	}

	seed, err := srv.dealerSeed(keyID)
	if err != nil {
		return nil, err
	}

	dealer, err := tdh.NewDealer(self, key.Members, key.Threshold, seed)
	if err != nil {
		return nil, fmt.Errorf("regenerating the dealer for %s: %w", keyID, err)
	}

	share, err := dealer.ShareFor(to)
	if err != nil {
		return nil, err
	}

	return contribution(self, dealer.Commitment(), share)
}

// recoverShare rebuilds this node's share of an existing key by asking every
// other member to replay what it dealt.
//
// This is repair, not rotation. The epoch is untouched, so no secret is
// re-encrypted and nothing is retired -- the node simply ends up holding again
// what it held before. The reconstruction is checked against the published group
// key, so a wrong or malicious contribution cannot be persisted as a share.
func (srv *Service) recoverShare(ctx context.Context, key *KeyMeta) error {
	self := srv.self()
	if !slices.Contains(key.Members, self) {
		return fmt.Errorf("this node is not a member of key %s", key.KeyID)
	}

	seed, err := srv.dealerSeed(key.KeyID)
	if err != nil {
		return err
	}

	dealer, err := tdh.NewDealer(self, key.Members, key.Threshold, seed)
	if err != nil {
		return fmt.Errorf("regenerating the dealer for %s: %w", key.KeyID, err)
	}

	for _, member := range key.Members {
		if member == self {
			continue
		}

		commitment, share, err := srv.askReplay(member, key.KeyID)
		if err != nil {
			return fmt.Errorf("replay from %s: %w", member, err)
		}

		if err := dealer.Receive(member, commitment, share); err != nil {
			return fmt.Errorf("replay from %s: %w", member, err)
		}
	}

	recovered, err := dealer.Finalize()
	if err != nil {
		return fmt.Errorf("rebuilding the share for %s: %w", key.KeyID, err)
	}

	// Check the share against the published verification vector, not against the
	// public key.
	//
	// The public key pins only the dealers' constant terms. A member replaying a
	// contribution can keep its constant term, change a higher coefficient, and
	// produce something that passes the Feldman check AND reproduces the exact
	// published public key -- while putting this node's share off the polynomial
	// every other member is on. That share then looks healthy on disk, so repair
	// never runs again, and the node poisons every threshold decryption it takes
	// part in. Only the full vector catches it.
	verification, err := key.VerificationVector()
	if err != nil {
		return err
	}
	if len(verification) == 0 {
		return fmt.Errorf("key %s carries no verification vector", key.KeyID)
	}

	public, err := recovered.Public.MarshalBinaryCompress()
	if err != nil {
		return fmt.Errorf("marshaling the rebuilt group key: %w", err)
	}
	if !bytes.Equal(public, key.Public) {
		return fmt.Errorf("the rebuilt share does not belong to key %s", key.KeyID)
	}
	if !tdh.VerifyShare(verification, recovered.Share) {
		return fmt.Errorf("the rebuilt share of %s does not sit on the group polynomial", key.KeyID)
	}

	return srv.shares.Put(ctx, key.KeyID, recovered.Share)
}

func (srv *Service) askReplay(member, keyID string) (secretsharing.SecretCommitment, secretsharing.Share, error) {
	to, err := peerCore.Decode(member)
	if err != nil {
		return nil, secretsharing.Share{}, err
	}

	api, err := srv.dealerAt(to)
	if err != nil {
		return nil, secretsharing.Share{}, err
	}

	ctx, cancel := context.WithTimeout(srv.ctx, callTimeout)
	defer cancel()
	resp, err := api.Replay(ctx, connect.NewRequest(&secretsv2.ReplayRequest{KeyId: keyID}))
	if err != nil {
		return nil, secretsharing.Share{}, err
	}

	return contributed(resp.Msg)
}
