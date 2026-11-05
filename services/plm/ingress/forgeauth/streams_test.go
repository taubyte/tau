package forgeauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/plm/ingress"
)

// feed is a driver whose only interesting face is its push feed.
type feed struct {
	host     string
	claims   []forge.PushClaim
	heads    []forge.PushClaim
	headsErr error
}

func (f *feed) Host() string                                { return f.host }
func (f *feed) Addressable(repokey.Key) error               { return nil }
func (f *feed) Bind(forge.Credential) (forge.Client, error) { return nil, errors.New("no client") }

func (f *feed) StreamPushes(_ context.Context, ready func(), emit func(forge.PushClaim)) error {
	ready()
	for _, c := range f.claims {
		emit(c)
	}
	return errors.New("ended")
}

func (f *feed) Heads(context.Context, repokey.Key) ([]forge.PushClaim, error) {
	return f.heads, f.headsErr
}

func TestStreamsAreTheRegistrysFeedsAsFrontendPushes(t *testing.T) {
	const host = "review.example"
	key := repokey.Key{Host: host, Owner: "acme", Name: "app"}
	sha := strings.Repeat("a", 40)
	f := &feed{
		host: host,
		claims: []forge.PushClaim{
			{Repository: key, Ref: "refs/heads/main", Commit: sha},
			{Repository: key, Ref: "main", Commit: sha},
		},
		heads: []forge.PushClaim{{Repository: key, Ref: "refs/heads/main", Commit: sha}},
	}
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: f})
	require.NoError(t, err)
	forges, err := New(reg)
	require.NoError(t, err)
	require.Equal(t, []string{host}, forges.Hosts())

	want := ingress.Push{Repository: key.String(), Ref: "refs/heads/main", Commit: sha}
	var got []ingress.Push
	var refused int
	err = forges.Stream(t.Context(), host, func() {}, func(p ingress.Push, err error) {
		if err != nil {
			require.Equal(t, ingress.Push{}, p)
			refused++
			return
		}
		got = append(got, p)
	})
	require.EqualError(t, err, "ended")
	require.Equal(t, []ingress.Push{want}, got)
	require.Equal(t, 1, refused)

	heads, err := forges.Heads(t.Context(), key.String())
	require.NoError(t, err)
	require.Equal(t, []ingress.Push{want}, heads)

	_, err = forges.Heads(t.Context(), "::not a key")
	require.ErrorContains(t, err, "names no repository")
	f.headsErr = errors.New("down")
	_, err = forges.Heads(t.Context(), key.String())
	require.ErrorContains(t, err, "down")
}
