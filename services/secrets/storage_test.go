package secrets

import (
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// TestCorruptEntriesAreSkippedOrRefused pins how the shared stores treat bytes
// they cannot decode: a listing skips them, so one bad entry cannot hide every
// good one, while a direct read of the bad entry is an error rather than a zero
// value.
func TestCorruptEntriesAreSkippedOrRefused(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()

	require.NoError(t, client.Set(ctx, Ref{Scope: Project("p", "e"), Name: "n"}, []byte("v")))
	good := mustActive(t, ctx, srv)
	ref := newest(ctx, srv, Ref{Scope: Project("p", "e"), Name: "n"})

	require.NoError(t, srv.db.Put(ctx, keyPrefix+"corrupt", []byte{0xff}))
	_, err := srv.keys.Get(ctx, "corrupt")
	require.ErrorContains(t, err, "unmarshaling key")
	keys, err := srv.keys.List(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 1, "a corrupt key record must be skipped, not listed")
	assert.Equal(t, good.KeyID, keys[0].KeyID)

	broken := Ref{Scope: Project("p", "e"), Name: "broken"}
	require.NoError(t, srv.db.Put(ctx, recordPrefix+"project/p/e/broken/cid", []byte{0xff}))
	require.NoError(t, srv.db.Put(ctx, recordPrefix+"project/p/e/no-cid", []byte{0xff}))
	versions, err := srv.records.Versions(ctx, broken)
	require.NoError(t, err)
	require.Empty(t, versions, "a corrupt version must be skipped, not listed")
	all, err := srv.records.All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []Ref{broken, ref.Ref}, all, "a path that is not a version names no secret")

	// The writer that sorts first is corrupt; Get must fall through to the
	// readable one rather than report the secret unreadable.
	_, writer, err := srv.wrappings.Get(ctx, ref, good.KeyID)
	require.NoError(t, err)
	require.NoError(t, srv.db.Put(ctx, wrappingPath(ref, good.KeyID, "0000"), []byte{0xff}))
	_, got, err := srv.wrappings.Get(ctx, ref, good.KeyID)
	require.NoError(t, err)
	assert.Equal(t, writer, got)
	_, err = srv.wrappings.GetFrom(ctx, ref, good.KeyID, "0000")
	require.ErrorContains(t, err, "unmarshaling wrapping")
	_, err = srv.wrappings.GetFrom(ctx, ref, good.KeyID, "nobody")
	require.ErrorContains(t, err, "no wrapping from nobody")
	_, err = srv.wrappings.GetFrom(ctx, version{}, good.KeyID, writer)
	require.ErrorIs(t, err, ErrInvalidRef)
	_, _, err = srv.wrappings.Get(ctx, version{}, good.KeyID)
	require.ErrorIs(t, err, ErrInvalidRef)
	_, err = srv.wrappings.KeyIDs(ctx, version{})
	require.ErrorIs(t, err, ErrInvalidRef)

	// The secret is still readable through all of it.
	value, err := client.Secret(ctx, "p", "e", "n")
	require.NoError(t, err)
	assert.Equal(t, "v", value)
}

func TestKeyMetaRefusesElementsItCannotDecode(t *testing.T) {
	bad := &KeyMeta{Public: []byte("x"), Verification: [][]byte{[]byte("x")}}
	_, err := bad.PublicKey()
	require.ErrorContains(t, err, "group public key")
	_, err = bad.VerificationVector()
	require.ErrorContains(t, err, "verification coefficient 0")
}

// TestAShareThatCannotBeOpenedIsNotAShare covers every way a stored share can
// be unusable. Each must be an error, because a node that took a damaged share
// for a real one would serve partials no threshold can combine.
func TestAShareThatCannotBeOpenedIsNotAShare(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()
	require.NoError(t, client.Set(ctx, Ref{Scope: Project("p", "e"), Name: "n"}, []byte("v")))
	key := mustActive(t, ctx, srv)

	share, err := srv.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)
	stored, err := srv.shares.StoredAt(ctx, key.KeyID)
	require.NoError(t, err)
	assert.False(t, stored.IsZero())

	put := func(t *testing.T, id string, plain []byte) {
		t.Helper()
		sealed, err := srv.shares.atRest.seal(plain)
		require.NoError(t, err)
		require.NoError(t, srv.shares.ds.Put(ctx, datastore.NewKey(sharePrefix+id), sealed))
	}
	value, err := share.Value.MarshalBinary()
	require.NoError(t, err)
	id, err := share.ID.MarshalBinary()
	require.NoError(t, err)
	encode := func(s storedShare) []byte {
		b, err := cbor.Marshal(s)
		require.NoError(t, err)
		return b
	}

	require.NoError(t, srv.shares.ds.Put(ctx, datastore.NewKey(sharePrefix+"unsealed"), []byte("plaintext on disk")))
	put(t, "not-cbor", []byte{0xff})
	put(t, "bad-id", encode(storedShare{ID: []byte("x"), Value: value}))
	put(t, "bad-value", encode(storedShare{ID: id, Value: []byte("x")}))

	for name, want := range map[string]string{
		"unsealed":  "share for key unsealed",
		"not-cbor":  "unmarshaling share",
		"bad-id":    "share id",
		"bad-value": "share value",
		"absent":    "getting share",
	} {
		_, err := srv.shares.Get(ctx, name)
		require.ErrorContains(t, err, want, name)
	}
	_, err = srv.shares.StoredAt(ctx, "absent")
	require.Error(t, err)

	require.NoError(t, srv.shares.Delete(ctx, "bad-id"))
	_, err = srv.shares.Get(ctx, "bad-id")
	require.ErrorContains(t, err, "getting share")

	require.NoError(t, (&shareStore{}).Close(), "a store that never opened closes cleanly")
}

// TestAPartialNeedsAShareThatOpens drives the damaged share through the
// service: the node must refuse to serve a partial rather than serve a wrong
// one, and must stop offering the key.
func TestAPartialNeedsAShareThatOpens(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()
	require.NoError(t, client.Set(ctx, Ref{Scope: Project("p", "e"), Name: "n"}, []byte("v")))
	key := mustActive(t, ctx, srv)
	ref := newest(ctx, srv, Ref{Scope: Project("p", "e"), Name: "n"})
	_, writer, err := srv.wrappings.Get(ctx, ref, key.KeyID)
	require.NoError(t, err)

	require.NoError(t, srv.shares.ds.Put(ctx, datastore.NewKey(sharePrefix+key.KeyID), []byte("damaged")))
	_, err = srv.Partial(ctx, ref, key.KeyID, writer)
	require.Error(t, err)
	_, err = srv.ActiveKey(ctx)
	require.Error(t, err)

	_, err = srv.recoverDEK(ctx, ref, key, &tdh.Wrapping{}, writer)
	require.ErrorContains(t, err, "collected 0 of 1")
}
