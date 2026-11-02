// Package tdh implements threshold DH-KEM over a prime-order group: a group key
// produced by a Pedersen (joint-Feldman) DKG, used to wrap a per-secret data key.
//
// Encryption needs only the group public key Y. Decryption needs `threshold`
// partial decryptions, each computed from one holder's share without revealing
// it. No participant ever holds the group scalar x -- every participant deals a
// polynomial, and x is the sum of all contributions.
//
// The scheme is identical at every size: a one-node cluster runs a 1-of-1
// threshold, not a different (simpler) scheme that upgrades later. Note that at
// threshold 1 the sole share *is* the group secret -- the threshold property is
// emergent at three or more holders, not a baseline guarantee.
package tdh

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/cloudflare/circl/group"
	"github.com/cloudflare/circl/math/polynomial"
	"github.com/cloudflare/circl/secretsharing"
)

// Group is the prime-order group everything here is defined over. Ristretto255
// has no cofactor, so there are no small-subgroup checks to get wrong.
var Group = group.Ristretto255

const (
	dekSize   = 32
	idDST     = "taubyte/secrets/tdh/participant-id/v1"
	secretDST = "taubyte/secrets/tdh/dealer-secret/v1"
	kekInfo   = "taubyte/secrets/tdh/kek/v1"
)

// ParticipantID derives a share index from a stable name (a peer id). Every node
// computes the same mapping, so there is no index-assignment protocol.
func ParticipantID(name string) (group.Scalar, error) {
	if name == "" {
		return nil, errors.New("tdh: empty participant name")
	}

	id := Group.HashToScalar([]byte(name), []byte(idDST))
	if id.IsZero() {
		return nil, fmt.Errorf("tdh: %q hashes to a zero share index", name)
	}

	return id, nil
}

// Dealer runs one participant's side of a DKG round. It is both a dealer (it
// splits its own random contribution) and a recipient (it collects one share
// from every participant, including itself).
type Dealer struct {
	self      string
	selfID    group.Scalar
	members   []string // sorted, unique, includes self
	threshold uint     // shares required to decrypt
	poly      polynomial.Polynomial
	commit    secretsharing.SecretCommitment
	got       map[string]contribution
}

type contribution struct {
	commit secretsharing.SecretCommitment
	share  group.Scalar
}

// NewDealer starts a DKG round. members must contain self, and threshold is the
// number of shares required to decrypt (1 <= threshold <= len(members)).
//
// The dealer's polynomial comes entirely from seed, which must be secret to
// this participant and specific to this round. Deriving it rather than drawing
// it fresh makes dealing idempotent: a node that restarts mid-round deals
// exactly what it dealt before instead of forking the group key.
//
// The coefficients are derived here rather than by secretsharing.New because
// this group's RandomScalar ignores the io.Reader it is given and reads the
// system source, which would make dealing unrepeatable.
func NewDealer(self string, members []string, threshold uint, seed []byte) (*Dealer, error) {
	if threshold == 0 {
		return nil, errors.New("tdh: threshold must be at least 1")
	}

	sorted := slices.Compact(slices.Sorted(slices.Values(members)))
	if len(sorted) != len(members) {
		return nil, errors.New("tdh: duplicate member")
	}
	if int(threshold) > len(sorted) {
		return nil, fmt.Errorf("tdh: threshold %d exceeds %d members", threshold, len(sorted))
	}
	if !slices.Contains(sorted, self) {
		return nil, fmt.Errorf("tdh: self %q is not a member", self)
	}

	if len(seed) == 0 {
		return nil, errors.New("tdh: empty dealer seed")
	}

	selfID, err := ParticipantID(self)
	if err != nil {
		return nil, err
	}

	// The polynomial has degree threshold-1, so threshold shares recover it.
	// Its constant term is this dealer's contribution to the group secret.
	coefficients := make([]group.Scalar, threshold)
	commit := make(secretsharing.SecretCommitment, threshold)
	for i := range coefficients {
		coefficients[i] = Group.HashToScalar(binary.BigEndian.AppendUint32(seed, uint32(i)), []byte(secretDST))
		commit[i] = Group.NewElement().MulGen(coefficients[i])
	}

	d := &Dealer{
		self:      self,
		selfID:    selfID,
		members:   sorted,
		threshold: threshold,
		poly:      polynomial.New(coefficients),
		commit:    commit,
		got:       make(map[string]contribution, len(sorted)),
	}

	// A dealer's own contribution never goes over the wire.
	own, err := d.ShareFor(self)
	if err != nil {
		return nil, err
	}
	d.got[self] = contribution{commit: d.commit, share: own.Value}

	return d, nil
}

// Commitment is this dealer's Feldman commitment, broadcast to every member.
func (d *Dealer) Commitment() secretsharing.SecretCommitment { return d.commit }

// ShareFor is the share this dealer owes member, sent to it and to nobody else.
func (d *Dealer) ShareFor(member string) (secretsharing.Share, error) {
	if !slices.Contains(d.members, member) {
		return secretsharing.Share{}, fmt.Errorf("tdh: %q is not a member", member)
	}

	id, err := ParticipantID(member)
	if err != nil {
		return secretsharing.Share{}, err
	}

	return secretsharing.Share{ID: id, Value: d.poly.Evaluate(id)}, nil
}

// Receive verifies and accumulates one dealer's contribution to this node. The
// Feldman commitment makes the share self-verifying: a dealer cannot hand out a
// share inconsistent with what it broadcast.
func (d *Dealer) Receive(from string, commit secretsharing.SecretCommitment, share secretsharing.Share) error {
	if !slices.Contains(d.members, from) {
		return fmt.Errorf("tdh: %q is not a member", from)
	}
	if _, dup := d.got[from]; dup {
		return fmt.Errorf("tdh: duplicate contribution from %q", from)
	}
	if share.ID == nil || !share.ID.IsEqual(d.selfID) {
		return fmt.Errorf("tdh: contribution from %q is addressed to another member", from)
	}
	if len(commit) != int(d.threshold) {
		return fmt.Errorf("tdh: commitment from %q has %d coefficients, want %d", from, len(commit), d.threshold)
	}
	if !secretsharing.Verify(d.threshold-1, share, commit) {
		return fmt.Errorf("tdh: share from %q does not match its commitment", from)
	}

	d.got[from] = contribution{commit: commit, share: share.Value}

	return nil
}

// Complete reports whether every member has contributed, so a caller can
// finalize as the last contribution lands rather than polling.
func (d *Dealer) Complete() bool { return len(d.got) == len(d.members) }

// Finalize combines every contribution into this node's key material. It needs
// one contribution per member: joint-Feldman has no complaint round, so an
// absent dealer aborts the round rather than being disqualified.
func (d *Dealer) Finalize() (*KeySet, error) {
	if len(d.got) != len(d.members) {
		return nil, fmt.Errorf("tdh: have %d of %d contributions", len(d.got), len(d.members))
	}

	x := Group.NewScalar()
	verification := make([]group.Element, d.threshold)
	for k := range verification {
		verification[k] = Group.Identity()
	}

	for _, m := range d.members {
		c := d.got[m]
		x.Add(x, c.share)
		for k := range verification {
			verification[k].Add(verification[k], c.commit[k])
		}
	}

	return &KeySet{
		Public:       verification[0],
		Verification: verification,
		Share:        secretsharing.Share{ID: d.selfID, Value: x},
		Threshold:    d.threshold,
		Members:      d.members,
	}, nil
}

// VerifyShare checks a share against the group's verification vector:
// x_i·G must equal Σ_k V[k]·i^k.
//
// This is the only check that binds a share to the whole polynomial. The group
// public key alone binds nothing but the constant terms, so a dealer that keeps
// its constant term and changes any higher coefficient produces a contribution
// that passes both the Feldman check and a public-key comparison while handing
// the recipient a share off the curve everyone else is on.
func VerifyShare(verification []group.Element, share secretsharing.Share) bool {
	if len(verification) == 0 || share.ID == nil || share.Value == nil {
		return false
	}

	// Horner over the committed coefficients, evaluated at the share's index.
	sum := Group.NewElement().Set(verification[len(verification)-1])
	for k := len(verification) - 2; k >= 0; k-- {
		sum.Mul(sum, share.ID)
		sum.Add(sum, verification[k])
	}

	return Group.NewElement().MulGen(share.Value).IsEqual(sum)
}

// KeySet is one node's output from a DKG round.
type KeySet struct {
	Public group.Element // Y = xG, all anyone needs to encrypt

	// Verification is the summed Feldman commitment, V[k] = the sum over dealers
	// of their k-th coefficient commitment. V[0] is Public. It is public data,
	// identical on every member, and it is what makes an individual share
	// checkable: without it, only the constant terms are pinned.
	Verification []group.Element

	Share     secretsharing.Share // this node's x_i; never leaves the node
	Threshold uint
	Members   []string
}

// Wrapping is a data key sealed to one epoch's group key. Rotating an epoch
// rewrites wrappings and nothing else: the ciphertext they unlock is never
// touched, so re-encryption never needs the plaintext.
type Wrapping struct {
	R          []byte
	WrappedDEK []byte
}

// Seal generates a data key and encrypts plaintext under it. The data key
// outlives any single epoch; only its wrapping is epoch-specific.
func Seal(plaintext, aad []byte) (dek, ciphertext []byte, err error) {
	dek = make([]byte, dekSize)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("tdh: reading random dek: %w", err)
	}

	if ciphertext, err = seal(dek, plaintext, aad); err != nil {
		return nil, nil, fmt.Errorf("tdh: sealing plaintext: %w", err)
	}

	return dek, ciphertext, nil
}

// Unseal decrypts with a data key recovered from a wrapping.
func Unseal(dek, ciphertext, aad []byte) ([]byte, error) {
	plaintext, err := open(dek, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("tdh: opening ciphertext: %w", err)
	}

	return plaintext, nil
}

// Wrap seals a data key to the group public key. aad binds the wrapping to the
// secret and epoch it belongs to, so one cannot be replayed onto another.
func Wrap(public group.Element, dek, aad []byte) (*Wrapping, error) {
	if public == nil || public.IsIdentity() {
		return nil, errors.New("tdh: degenerate group public key")
	}
	if len(dek) != dekSize {
		return nil, fmt.Errorf("tdh: data key must be %d bytes", dekSize)
	}

	r := Group.RandomNonZeroScalar(rand.Reader)
	ephemeral := Group.NewElement().MulGen(r)
	shared := Group.NewElement().Mul(public, r)

	kek, err := deriveKEK(shared, ephemeral, public)
	if err != nil {
		return nil, err
	}

	wrapped, err := seal(kek, dek, aad)
	if err != nil {
		return nil, fmt.Errorf("tdh: wrapping dek: %w", err)
	}

	ephemeralBytes, err := ephemeral.MarshalBinaryCompress()
	if err != nil {
		return nil, fmt.Errorf("tdh: marshaling ephemeral key: %w", err)
	}

	return &Wrapping{R: ephemeralBytes, WrappedDEK: wrapped}, nil
}

// Partial is one holder's contribution to opening one wrapping. It reveals
// nothing about the holder's share.
type Partial struct {
	ID []byte
	D  []byte
}

// PartialDecrypt computes this holder's x_i*R for the wrapping. The holder must
// never apply its share to anything but a stored wrapping.
func PartialDecrypt(share secretsharing.Share, w *Wrapping) (*Partial, error) {
	if share.ID == nil || share.Value == nil {
		return nil, errors.New("tdh: incomplete share")
	}

	ephemeral, err := unmarshalElement(w.R)
	if err != nil {
		return nil, fmt.Errorf("tdh: wrapping ephemeral key: %w", err)
	}

	d, err := Group.NewElement().Mul(ephemeral, share.Value).MarshalBinaryCompress()
	if err != nil {
		return nil, fmt.Errorf("tdh: marshaling partial: %w", err)
	}

	id, err := share.ID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("tdh: marshaling share id: %w", err)
	}

	return &Partial{ID: id, D: d}, nil
}

// Unwrap recovers the data key from `threshold` partials by Lagrange
// interpolation in the exponent. Extra partials are ignored, so a caller may
// hand over everything it collected.
func Unwrap(w *Wrapping, public group.Element, partials []*Partial, threshold uint, aad []byte) ([]byte, error) {
	if threshold == 0 {
		return nil, errors.New("tdh: threshold must be at least 1")
	}
	if uint(len(partials)) < threshold {
		return nil, fmt.Errorf("tdh: have %d partials, need %d", len(partials), threshold)
	}

	ids := make([]group.Scalar, threshold)
	points := make([]group.Element, threshold)
	for i, p := range partials[:threshold] {
		id := Group.NewScalar()
		if err := id.UnmarshalBinary(p.ID); err != nil {
			return nil, fmt.Errorf("tdh: partial %d id: %w", i, err)
		}
		if id.IsZero() {
			return nil, fmt.Errorf("tdh: partial %d has a zero share index", i)
		}
		// Lagrange divides by (id_j - id_i); a repeat would invert zero.
		if slices.ContainsFunc(ids[:i], id.IsEqual) {
			return nil, fmt.Errorf("tdh: partial %d repeats an earlier share index", i)
		}

		point, err := unmarshalElement(p.D)
		if err != nil {
			return nil, fmt.Errorf("tdh: partial %d: %w", i, err)
		}

		ids[i], points[i] = id, point
	}

	zero := Group.NewScalar().SetUint64(0)
	shared := Group.Identity()
	for j := range ids {
		lambda := polynomial.LagrangeBase(uint(j), ids, zero)
		shared.Add(shared, Group.NewElement().Mul(points[j], lambda))
	}

	ephemeral, err := unmarshalElement(w.R)
	if err != nil {
		return nil, fmt.Errorf("tdh: wrapping ephemeral key: %w", err)
	}

	kek, err := deriveKEK(shared, ephemeral, public)
	if err != nil {
		return nil, err
	}

	// Any wrong partial lands on a wrong KEK, which this rejects. Identifying
	// *which* holder was wrong needs a proof the partial does not carry.
	dek, err := open(kek, w.WrappedDEK, aad)
	if err != nil {
		return nil, fmt.Errorf("tdh: unwrapping dek: %w", err)
	}

	return dek, nil
}

// deriveKEK binds the key-encryption key to the ephemeral and group public
// keys, so a shared secret recovered for one envelope unwraps only that one.
func deriveKEK(shared, ephemeral, public group.Element) ([]byte, error) {
	if shared.IsIdentity() {
		return nil, errors.New("tdh: degenerate shared secret")
	}

	ikm := make([]byte, 0, 3*Group.Params().CompressedElementLength)
	for _, e := range []group.Element{shared, ephemeral, public} {
		b, err := e.MarshalBinaryCompress()
		if err != nil {
			return nil, fmt.Errorf("tdh: marshaling kek input: %w", err)
		}
		ikm = append(ikm, b...)
	}

	kek, err := hkdf.Key(sha256.New, ikm, nil, kekInfo, dekSize)
	if err != nil {
		return nil, fmt.Errorf("tdh: deriving kek: %w", err)
	}

	return kek, nil
}

func unmarshalElement(b []byte) (group.Element, error) {
	e := Group.NewElement()
	if err := e.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	if e.IsIdentity() {
		return nil, errors.New("identity element")
	}

	return e, nil
}

func seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("short ciphertext")
	}

	nonce, body := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]

	return aead.Open(nil, nonce, body, aad)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCM(block)
}
