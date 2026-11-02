package tdh

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cloudflare/circl/group"
	"github.com/cloudflare/circl/math/polynomial"
	"github.com/cloudflare/circl/secretsharing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedFor stands in for the per-node secret a real deployment derives from its
// identity: distinct per participant, stable across calls.
func seedFor(t *testing.T, name string) []byte {
	t.Helper()

	return []byte("seed/" + t.Name() + "/" + name)
}

// rounds numbers DKG runs so two runs in one test get different key material,
// the way two real rounds would.
var rounds atomic.Uint64

// runDKG drives a full DKG round between n in-memory participants and returns
// each one's key set, in member order.
func runDKG(t *testing.T, n int, threshold uint) []*KeySet {
	t.Helper()

	members := make([]string, n)
	for i := range members {
		members[i] = fmt.Sprintf("node-%d", i)
	}

	round := rounds.Add(1)

	dealers := make([]*Dealer, n)
	for i, m := range members {
		d, err := NewDealer(m, members, threshold, fmt.Appendf(nil, "seed/%d/%s", round, m))
		require.NoError(t, err)
		dealers[i] = d
	}

	// Every dealer broadcasts its commitment and sends one private share to
	// every other member.
	for i, from := range dealers {
		for j, to := range dealers {
			if i == j {
				continue // a dealer's own contribution never goes over the wire
			}
			share, err := from.ShareFor(members[j])
			require.NoError(t, err)
			require.NoError(t, to.Receive(members[i], from.Commitment(), share))
		}
	}

	keys := make([]*KeySet, n)
	for i, d := range dealers {
		k, err := d.Finalize()
		require.NoError(t, err)
		keys[i] = k
	}

	// Every participant must land on the same group public key.
	for _, k := range keys[1:] {
		require.True(t, k.Public.IsEqual(keys[0].Public), "participants disagree on the group key")
	}

	return keys
}

func partials(t *testing.T, keys []*KeySet, w *Wrapping) []*Partial {
	t.Helper()

	out := make([]*Partial, len(keys))
	for i, k := range keys {
		p, err := PartialDecrypt(k.Share, w)
		require.NoError(t, err)
		out[i] = p
	}

	return out
}

// encrypt seals a plaintext and wraps its data key in one step, the way a
// client does.
func encrypt(t *testing.T, public group.Element, plaintext, aad []byte) (*Wrapping, []byte) {
	t.Helper()

	dek, ciphertext, err := Seal(plaintext, aad)
	require.NoError(t, err)

	w, err := Wrap(public, dek, aad)
	require.NoError(t, err)

	return w, ciphertext
}

// decrypt reverses encrypt.
func decrypt(w *Wrapping, ciphertext []byte, public group.Element, parts []*Partial, threshold uint, aad []byte) ([]byte, error) {
	dek, err := Unwrap(w, public, parts, threshold, aad)
	if err != nil {
		return nil, err
	}

	return Unseal(dek, ciphertext, aad)
}

func TestRoundTrip(t *testing.T) {
	// The protocol must not change with cluster size: one node runs the same
	// scheme at threshold 1 that twenty run at threshold 11.
	type testCase struct{ n, threshold int }
	for _, tc := range []testCase{
		{1, 1}, {2, 1}, {2, 2}, {3, 2}, {3, 3}, {5, 3}, {7, 4},
	} {
		t.Run(fmt.Sprintf("%dof%d", tc.threshold, tc.n), func(t *testing.T) {
			keys := runDKG(t, tc.n, uint(tc.threshold))
			plaintext := []byte("owner api key")
			aad := []byte("secret/tenancy/owner-key")

			w, ciphertext := encrypt(t, keys[0].Public, plaintext, aad)
			require.NotContains(t, string(ciphertext), string(plaintext))

			got, err := decrypt(w, ciphertext, keys[0].Public, partials(t, keys, w), uint(tc.threshold), aad)
			require.NoError(t, err)
			assert.Equal(t, plaintext, got)
		})
	}
}

func TestAnyThresholdSubsetRecovers(t *testing.T) {
	const n, threshold = 5, 3
	keys := runDKG(t, n, threshold)
	plaintext := []byte("recovered by any quorum")

	w, ciphertext := encrypt(t, keys[0].Public, plaintext, nil)
	all := partials(t, keys, w)

	// Walk every 3-subset of the 5 holders.
	for i := range n {
		for j := i + 1; j < n; j++ {
			for k := j + 1; k < n; k++ {
				got, err := decrypt(w, ciphertext, keys[0].Public, []*Partial{all[i], all[j], all[k]}, threshold, nil)
				require.NoErrorf(t, err, "subset %d,%d,%d", i, j, k)
				assert.Equal(t, plaintext, got)
			}
		}
	}
}

func TestBelowThresholdFails(t *testing.T) {
	keys := runDKG(t, 5, 3)

	w, ciphertext := encrypt(t, keys[0].Public, []byte("quorum or nothing"), nil)
	all := partials(t, keys, w)

	_, err := decrypt(w, ciphertext, keys[0].Public, all[:2], 3, nil)
	require.ErrorContains(t, err, "need 3")

	// Padding the quorum with a repeated holder must not substitute for a
	// distinct one -- Lagrange would invert zero.
	_, err = decrypt(w, ciphertext, keys[0].Public, []*Partial{all[0], all[1], all[1]}, 3, nil)
	require.ErrorContains(t, err, "repeats an earlier share index")
}

func TestWrongPartialsFail(t *testing.T) {
	keys := runDKG(t, 3, 2)
	other := runDKG(t, 3, 2)

	w, ciphertext := encrypt(t, keys[0].Public, []byte("bound to one key"), nil)

	// Partials from a different DKG round reach a different shared secret,
	// which the AEAD rejects rather than returning garbage.
	_, err := decrypt(w, ciphertext, keys[0].Public, partials(t, other, w), 2, nil)
	require.ErrorContains(t, err, "unwrapping dek")

	// So does one good partial and one foreign one.
	mixed := []*Partial{partials(t, keys, w)[0], partials(t, other, w)[1]}
	_, err = decrypt(w, ciphertext, keys[0].Public, mixed, 2, nil)
	require.ErrorContains(t, err, "unwrapping dek")
}

func TestAADIsBound(t *testing.T) {
	keys := runDKG(t, 3, 2)

	w, ciphertext := encrypt(t, keys[0].Public, []byte("bound to its name"), []byte("secret/a"))

	_, err := decrypt(w, ciphertext, keys[0].Public, partials(t, keys, w), 2, []byte("secret/b"))
	require.ErrorContains(t, err, "unwrapping dek")
}

func TestTamperedWrappingFails(t *testing.T) {
	keys := runDKG(t, 3, 2)

	base, baseCiphertext := encrypt(t, keys[0].Public, []byte("do not flip my bits"), nil)

	for name, corrupt := range map[string]func(*Wrapping, []byte){
		"ciphertext": func(_ *Wrapping, c []byte) { c[len(c)-1] ^= 1 },
		"wrapped":    func(w *Wrapping, _ []byte) { w.WrappedDEK[len(w.WrappedDEK)-1] ^= 1 },
		"ephemeral":  func(w *Wrapping, _ []byte) { w.R[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			w := &Wrapping{R: bytes.Clone(base.R), WrappedDEK: bytes.Clone(base.WrappedDEK)}
			ciphertext := bytes.Clone(baseCiphertext)
			corrupt(w, ciphertext)

			_, err := decrypt(w, ciphertext, keys[0].Public, partials(t, keys, base), 2, nil)
			require.Error(t, err)
		})
	}
}

func TestReceiveRejectsBadContributions(t *testing.T) {
	members := []string{"a", "b", "c"}
	const threshold = 2

	newRound := func(t *testing.T) (*Dealer, *Dealer) {
		t.Helper()
		a, err := NewDealer("a", members, threshold, seedFor(t, "a"))
		require.NoError(t, err)
		b, err := NewDealer("b", members, threshold, seedFor(t, "b"))
		require.NoError(t, err)
		return a, b
	}

	t.Run("share inconsistent with commitment", func(t *testing.T) {
		a, b := newRound(t)
		share, err := b.ShareFor("a")
		require.NoError(t, err)
		share.Value = Group.HashToScalar([]byte("well formed but wrong"), []byte("test"))

		require.ErrorContains(t, a.Receive("b", b.Commitment(), share), "does not match its commitment")
	})

	t.Run("share addressed to another member", func(t *testing.T) {
		a, b := newRound(t)
		share, err := b.ShareFor("c")
		require.NoError(t, err)

		require.ErrorContains(t, a.Receive("b", b.Commitment(), share), "addressed to another member")
	})

	t.Run("unknown dealer", func(t *testing.T) {
		a, b := newRound(t)
		share, err := b.ShareFor("a")
		require.NoError(t, err)

		require.ErrorContains(t, a.Receive("intruder", b.Commitment(), share), "not a member")
	})

	t.Run("duplicate dealer", func(t *testing.T) {
		a, b := newRound(t)
		share, err := b.ShareFor("a")
		require.NoError(t, err)

		require.NoError(t, a.Receive("b", b.Commitment(), share))
		require.ErrorContains(t, a.Receive("b", b.Commitment(), share), "duplicate contribution")
	})

	t.Run("commitment of the wrong degree", func(t *testing.T) {
		a, b := newRound(t)
		share, err := b.ShareFor("a")
		require.NoError(t, err)

		require.ErrorContains(t, a.Receive("b", b.Commitment()[:1], share), "coefficients, want 2")
	})

	t.Run("finalize before every contribution", func(t *testing.T) {
		a, _ := newRound(t)
		_, err := a.Finalize()
		require.ErrorContains(t, err, "have 1 of 3 contributions")
	})
}

func TestDealingIsDeterministic(t *testing.T) {
	members := []string{"a", "b", "c"}

	// A node that restarts mid-round must re-deal exactly what it dealt before,
	// or members that kept the first contribution and members that took the
	// second would finalize different group keys.
	first, err := NewDealer("a", members, 2, seedFor(t, "a"))
	require.NoError(t, err)
	again, err := NewDealer("a", members, 2, seedFor(t, "a"))
	require.NoError(t, err)

	for _, member := range members {
		want, err := first.ShareFor(member)
		require.NoError(t, err)
		got, err := again.ShareFor(member)
		require.NoError(t, err)
		assert.True(t, want.Value.IsEqual(got.Value), "share for %s changed between dealers", member)
	}

	require.Len(t, again.Commitment(), 2)
	for i, c := range first.Commitment() {
		assert.True(t, c.IsEqual(again.Commitment()[i]), "commitment coefficient %d changed", i)
	}

	// A different seed must still give a different polynomial.
	other, err := NewDealer("a", members, 2, seedFor(t, "a-other"))
	require.NoError(t, err)
	assert.False(t, first.Commitment()[0].IsEqual(other.Commitment()[0]))
}

func TestNewDealerRejectsBadRosters(t *testing.T) {
	type testCase struct {
		self      string
		members   []string
		threshold uint
		wantErr   string
	}
	for name, tc := range map[string]testCase{
		"zero threshold":       {"a", []string{"a", "b"}, 0, "at least 1"},
		"threshold above n":    {"a", []string{"a", "b"}, 3, "exceeds 2 members"},
		"self not a member":    {"z", []string{"a", "b"}, 1, `"z" is not a member`},
		"duplicate member":     {"a", []string{"a", "a", "b"}, 1, "duplicate member"},
		"empty participant id": {"", []string{""}, 1, "empty participant name"},
		"empty seed":           {"a", []string{"a"}, 1, "empty dealer seed"},
	} {
		t.Run(name, func(t *testing.T) {
			seed := seedFor(t, tc.self)
			if name == "empty seed" {
				seed = nil
			}

			_, err := NewDealer(tc.self, tc.members, tc.threshold, seed)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestParticipantIDIsStableAndDistinct(t *testing.T) {
	a, err := ParticipantID("node-a")
	require.NoError(t, err)
	again, err := ParticipantID("node-a")
	require.NoError(t, err)
	b, err := ParticipantID("node-b")
	require.NoError(t, err)

	assert.True(t, a.IsEqual(again), "same name must map to the same share index")
	assert.False(t, a.IsEqual(b), "distinct names must map to distinct share indices")
	assert.False(t, a.IsZero())
}

func TestWrapRejectsDegenerateKey(t *testing.T) {
	dek, _, err := Seal([]byte("x"), nil)
	require.NoError(t, err)

	_, err = Wrap(Group.Identity(), dek, nil)
	require.ErrorContains(t, err, "degenerate group public key")

	_, err = Wrap(nil, dek, nil)
	require.ErrorContains(t, err, "degenerate group public key")
}

func TestRewrapKeepsCiphertext(t *testing.T) {
	old := runDKG(t, 3, 2)
	new := runDKG(t, 3, 2)
	plaintext := []byte("carried across an epoch")

	w, ciphertext := encrypt(t, old[0].Public, plaintext, []byte("secret/a"))

	// Recover only the data key, under the old epoch.
	dek, err := Unwrap(w, old[0].Public, partials(t, old, w), 2, []byte("secret/a"))
	require.NoError(t, err)

	rewrapped, err := Wrap(new[0].Public, dek, []byte("secret/a"))
	require.NoError(t, err)

	// The untouched ciphertext opens under the new epoch.
	got, err := decrypt(rewrapped, ciphertext, new[0].Public, partials(t, new, rewrapped), 2, []byte("secret/a"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)

	// And the old epoch's holders can no longer open the new wrapping.
	_, err = Unwrap(rewrapped, new[0].Public, partials(t, old, rewrapped), 2, []byte("secret/a"))
	require.ErrorContains(t, err, "unwrapping dek")
}

func TestPartialDecryptRejectsDegenerateEphemeral(t *testing.T) {
	keys := runDKG(t, 2, 1)

	identity, err := Group.Identity().MarshalBinaryCompress()
	require.NoError(t, err)

	_, err = PartialDecrypt(keys[0].Share, &Wrapping{R: identity})
	require.ErrorContains(t, err, "identity element")
}

func TestVerifyShareCatchesAForgedContribution(t *testing.T) {
	members := []string{"a", "b", "c"}
	const threshold = 2

	seeds := map[string][]byte{"a": seedFor(t, "a"), "b": seedFor(t, "b"), "c": seedFor(t, "c")}
	dealers := map[string]*Dealer{}
	for _, m := range members {
		d, err := NewDealer(m, members, threshold, seeds[m])
		require.NoError(t, err)
		dealers[m] = d
	}

	// The honest round, from a's point of view.
	honest, err := NewDealer("a", members, threshold, seeds["a"])
	require.NoError(t, err)
	for _, m := range []string{"b", "c"} {
		share, err := dealers[m].ShareFor("a")
		require.NoError(t, err)
		require.NoError(t, honest.Receive(m, dealers[m].Commitment(), share))
	}
	real, err := honest.Finalize()
	require.NoError(t, err)
	require.True(t, VerifyShare(real.Verification, real.Share), "an honest share must verify")

	// b forges: same constant term, different first-degree coefficient.
	b0 := Group.HashToScalar(binary.BigEndian.AppendUint32(seeds["b"], 0), []byte(secretDST))
	coefficients := []group.Scalar{b0, Group.HashToScalar([]byte("attacker chosen"), []byte(secretDST))}
	forgedCommit := make(secretsharing.SecretCommitment, len(coefficients))
	for i, c := range coefficients {
		forgedCommit[i] = Group.NewElement().MulGen(c)
	}
	idA, err := ParticipantID("a")
	require.NoError(t, err)
	forged := secretsharing.Share{ID: idA, Value: polynomial.New(coefficients).Evaluate(idA)}

	victim, err := NewDealer("a", members, threshold, seeds["a"])
	require.NoError(t, err)

	// The Feldman check does NOT catch it: the pair is self-consistent.
	require.NoError(t, victim.Receive("b", forgedCommit, forged),
		"the forged contribution should pass the per-dealer check -- that is the point")

	shareC, err := dealers["c"].ShareFor("a")
	require.NoError(t, err)
	require.NoError(t, victim.Receive("c", dealers["c"].Commitment(), shareC))

	rebuilt, err := victim.Finalize()
	require.NoError(t, err)

	// The public key does NOT catch it either.
	assert.True(t, rebuilt.Public.IsEqual(real.Public),
		"the forgery reproduces the published group key, so that check cannot catch it")
	assert.False(t, rebuilt.Share.Value.IsEqual(real.Share.Value),
		"the rebuilt share should be corrupted")

	// The verification vector does.
	assert.False(t, VerifyShare(real.Verification, rebuilt.Share),
		"a share off the group polynomial must fail verification")
}
