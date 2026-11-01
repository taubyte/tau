package forge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

type feed struct {
	plain
	claims   []PushClaim
	heads    []PushClaim
	headsErr error
	err      error
}

func (f *feed) Addressable(k repokey.Key) error {
	if k.Name == "" {
		return errors.New("no name")
	}
	return nil
}

func (f *feed) StreamPushes(_ context.Context, ready func(), emit func(PushClaim)) error {
	ready()
	for _, c := range f.claims {
		emit(c)
	}
	return f.err
}

func (f *feed) Heads(context.Context, repokey.Key) ([]PushClaim, error) {
	return f.heads, f.headsErr
}

var (
	sha1A = strings.Repeat("a", 40)
	zero  = strings.Repeat("0", 40)
)

func TestAFeedIsCheckedTheWayADeliveryIs(t *testing.T) {
	const host = "review.example"
	key := repokey.Key{Host: host, Owner: "acme", Name: "app"}
	other := repokey.Key{Host: "elsewhere.example", Owner: "acme", Name: "app"}
	f := &feed{plain: plain{host: host}, err: errors.New("feed broke"), claims: []PushClaim{
		{Repository: key, Ref: "refs/heads/main", Commit: sha1A},
		{Repository: other, Ref: "refs/heads/main", Commit: sha1A},
		{Repository: repokey.Key{Host: host, Owner: "acme"}, Ref: "refs/heads/main", Commit: sha1A},
		{Repository: key, Ref: "main", Commit: sha1A},
		{Repository: key, Ref: "refs/heads/main", Commit: zero},
		{Repository: key, Ref: "refs/heads/main", Commit: "abc"},
	}}
	reg, err := New(Policy{}, Binding{Host: host, Driver: f}, Binding{Host: "plain.example", Driver: &plain{host: "plain.example"}})
	require.NoError(t, err)
	require.Equal(t, []string{host}, reg.EventHosts())

	readied := false
	var pushes []Push
	var refused int
	err = reg.StreamPushes(t.Context(), host, func() { readied = true }, func(p Push, err error) {
		if err != nil {
			refused++
			return
		}
		pushes = append(pushes, p)
	})
	require.EqualError(t, err, "feed broke")
	require.True(t, readied)
	require.Equal(t, []Push{{Key: key, Ref: "refs/heads/main", Commit: sha1A}}, pushes)
	require.Equal(t, 4, refused, "another host, an unaddressable key, a bare ref and a short commit; a deletion is silent")

	sess, err := reg.Host(host, Token("t"))
	require.NoError(t, err)
	require.True(t, sess.Capabilities().Has(CapPushEvents))

	var un *UnsupportedError
	require.ErrorAs(t, reg.StreamPushes(t.Context(), "plain.example", func() {}, func(Push, error) {}), &un)
	_, err = reg.Heads(t.Context(), repokey.Key{Host: "plain.example", Owner: "a", Name: "b"})
	require.ErrorAs(t, err, &un)
	var none *NoDriverError
	require.ErrorAs(t, reg.StreamPushes(t.Context(), "nobody.example", func() {}, func(Push, error) {}), &none)
	_, err = reg.Heads(t.Context(), repokey.Key{Host: "nobody.example", Owner: "a", Name: "b"})
	require.ErrorAs(t, err, &none)
}

func TestHeadsKeepOnlyTheRepositorysOwnValidHeads(t *testing.T) {
	const host = "review.example"
	key := repokey.Key{Host: host, Owner: "acme", Name: "app"}
	f := &feed{plain: plain{host: host}, heads: []PushClaim{
		{Repository: key, Ref: "refs/heads/main", Commit: sha1A},
		{Repository: repokey.Key{Host: host, Owner: "acme", Name: "other"}, Ref: "refs/heads/main", Commit: sha1A},
		{Repository: key, Ref: "refs/heads/bad..ref", Commit: sha1A},
	}}
	reg, err := New(Policy{}, Binding{Host: host, Driver: f})
	require.NoError(t, err)
	heads, err := reg.Heads(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, []Push{{Key: key, Ref: "refs/heads/main", Commit: sha1A}}, heads)

	f.headsErr = errors.New("down")
	_, err = reg.Heads(t.Context(), key)
	require.EqualError(t, err, "down")
}
