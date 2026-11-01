package tdh

import (
	"testing"

	"github.com/cloudflare/circl/secretsharing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestARoundCompletesOnlyWithEveryContribution(t *testing.T) {
	members := []string{"a", "b", "c"}
	dealers := make(map[string]*Dealer, len(members))
	for _, m := range members {
		d, err := NewDealer(m, members, 2, []byte("seed/"+m))
		require.NoError(t, err)
		dealers[m] = d
	}

	a := dealers["a"]
	assert.False(t, a.Complete(), "a dealer's own contribution is not a round")
	_, err := a.Finalize()
	require.ErrorContains(t, err, "have 1 of 3")

	_, err = a.ShareFor("stranger")
	require.ErrorContains(t, err, "not a member")

	for _, from := range []string{"b", "c"} {
		share, err := dealers[from].ShareFor("a")
		require.NoError(t, err)
		require.NoError(t, a.Receive(from, dealers[from].Commitment(), share))
	}
	assert.True(t, a.Complete())
	_, err = a.Finalize()
	require.NoError(t, err)
}

func TestSealingRefusesMalformedKeysAndCiphertexts(t *testing.T) {
	keys := runDKG(t, 2, 1)

	_, err := Wrap(keys[0].Public, []byte("short dek"), nil)
	require.ErrorContains(t, err, "data key must be")

	dek, ciphertext, err := Seal([]byte("v"), []byte("aad"))
	require.NoError(t, err)

	_, err = Unseal(dek, ciphertext[:3], []byte("aad"))
	require.ErrorContains(t, err, "short ciphertext")
	_, err = Unseal([]byte("not an aes key"), ciphertext, []byte("aad"))
	require.Error(t, err)

	got, err := Unseal(dek, ciphertext, []byte("aad"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), got)
}

func TestPartialsThatCannotBeUsedAreRefused(t *testing.T) {
	keys := runDKG(t, 3, 2)
	w, _ := encrypt(t, keys[0].Public, []byte("v"), nil)
	parts := partials(t, keys, w)

	_, err := PartialDecrypt(secretsharing.Share{}, w)
	require.ErrorContains(t, err, "incomplete share")
	_, err = PartialDecrypt(keys[0].Share, &Wrapping{R: []byte("garbage")})
	require.ErrorContains(t, err, "ephemeral")

	zero, err := Group.NewScalar().MarshalBinary()
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		w         *Wrapping
		parts     []*Partial
		threshold uint
		want      string
	}{
		"zero threshold": {w, parts, 0, "at least 1"},
		"bad index":      {w, []*Partial{{ID: []byte("x"), D: parts[0].D}, parts[1]}, 2, "partial 0 id"},
		"zero index":     {w, []*Partial{{ID: zero, D: parts[0].D}, parts[1]}, 2, "zero share index"},
		"repeated index": {w, []*Partial{parts[0], parts[0]}, 2, "repeats"},
		"bad point":      {w, []*Partial{parts[0], {ID: parts[1].ID, D: []byte("x")}}, 2, "partial 1"},
		"bad ephemeral":  {&Wrapping{R: []byte("x"), WrappedDEK: w.WrappedDEK}, parts, 2, "ephemeral"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Unwrap(tc.w, keys[0].Public, tc.parts, tc.threshold, nil)
			require.ErrorContains(t, err, tc.want)
		})
	}

	// The same partials, well formed, do open it.
	_, err = Unwrap(w, keys[0].Public, parts, 2, nil)
	require.NoError(t, err)
}
