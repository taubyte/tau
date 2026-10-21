package forgeauth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

func fakeForges(t *testing.T, sh fake.Shape) (*fake.Forge, *Forges) {
	t.Helper()
	f := fake.New(sh)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	fa, err := New(reg)
	require.NoError(t, err)
	return f, fa
}

func TestListAndCreate(t *testing.T) {
	f, fa := fakeForges(t, fake.GitHubLike)
	ctx := t.Context()

	got, err := fa.List(ctx, f.TokenString(), f.Host())
	require.NoError(t, err)
	var keys []string
	for _, r := range got {
		keys = append(keys, r.Repository)
	}
	require.Contains(t, keys, f.Keys().Writable.String())

	made, err := fa.Create(ctx, f.TokenString(), f.Keys().Creatable, frontend.NewRepository{Private: true})
	require.NoError(t, err)
	require.Equal(t, f.Keys().Creatable.String(), made.Repository)
	_, err = fa.Create(ctx, f.TokenString(), f.Keys().Creatable, frontend.NewRepository{})
	require.ErrorIs(t, err, frontend.ErrExists, "a repository is created once")

	_, err = fa.List(ctx, f.BadTokenString(), f.Host())
	require.ErrorIs(t, err, frontend.ErrBadCredential)
	_, err = fa.List(ctx, f.TokenString(), "nowhere.example")
	require.ErrorIs(t, err, frontend.ErrUnknownForge)
	_, err = fa.Create(ctx, f.TokenString(), repokey.Key{Host: "nowhere.example", Owner: "a", Name: "b"}, frontend.NewRepository{})
	require.ErrorIs(t, err, frontend.ErrUnknownForge)
	_, err = fa.List(ctx, "", f.Host())
	require.Error(t, err)
}

// A forge without the face says so, as ErrUnsupported, rather than an empty
// answer.
func TestAMinimalForgeSaysUnsupported(t *testing.T) {
	f, fa := fakeForges(t, fake.Minimal)
	ctx := t.Context()
	_, err := fa.List(ctx, f.TokenString(), f.Host())
	require.ErrorIs(t, err, frontend.ErrUnsupported)
	_, err = fa.Create(ctx, f.TokenString(), f.Keys().Creatable, frontend.NewRepository{})
	require.ErrorIs(t, err, frontend.ErrUnsupported)
	_, err = fa.Remote(ctx, f.TokenString(), f.Keys().Writable)
	require.ErrorIs(t, err, frontend.ErrUnsupported)
}

// Read access is read: a public repository the caller holds no grant on is
// readable, one they hold nothing on is not, and a refused credential and an
// outage stay what they are.
func TestReadAccess(t *testing.T) {
	f, fa := fakeForges(t, fake.GitHubLike)
	ctx := t.Context()
	for key, want := range map[repokey.Key]frontend.Access{
		f.Keys().Writable: frontend.AccessGranted,
		f.Keys().ReadOnly: frontend.AccessGranted,
		f.Keys().NoAccess: frontend.AccessDenied,
		f.Keys().Missing:  frontend.AccessDenied,
	} {
		got, err := fa.ReadAccess(ctx, f.TokenString(), key)
		require.NoError(t, err, key.String())
		require.Equal(t, want, got, key.String())
	}
	got, err := fa.ReadAccess(ctx, f.BadTokenString(), f.Keys().Writable)
	require.NoError(t, err)
	require.Equal(t, frontend.AccessUnauthenticated, got)
	got, err = fa.ReadAccess(ctx, "", f.Keys().Writable)
	require.NoError(t, err)
	require.Equal(t, frontend.AccessUnauthenticated, got)
	_, err = fa.ReadAccess(ctx, f.TokenString(), repokey.Key{Host: "nowhere.example", Owner: "a", Name: "b"})
	require.Error(t, err)
	f.Break()
	_, err = fa.ReadAccess(ctx, f.TokenString(), f.Keys().Writable)
	require.Error(t, err)
	_, err = fa.Remote(ctx, f.TokenString(), f.Keys().Writable)
	require.Error(t, err, "who commits is asked of the forge too")
}

// The remote is the forge's own clone address, and its transport carries the
// caller's credential to that host and to no other.
func TestRemoteCarriesTheCredentialToItsHostAlone(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	f, fa := fakeForges(t, fake.GitHubLike)
	key := f.Keys().Writable
	remote, err := fa.Remote(t.Context(), f.TokenString(), key)
	require.NoError(t, err)
	require.Equal(t, key.Host, remote.URL.Host)
	require.Equal(t, "fake-user", remote.Author)
	require.Equal(t, "fake-user@example.invalid", remote.Email)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	_, err = remote.Transport.RoundTrip(req)
	var leak *forge.CredentialLeakError
	require.ErrorAs(t, err, &leak)
	require.Empty(t, seen, "nothing reached another host")

	f2, fa2 := fakeForges(t, fake.Shape{Host: "127.0.0.1:" + srv.URL[len("http://127.0.0.1:"):], Scheme: forge.SchemeHubSHA256, PayloadCarriesCloneURL: true})
	remote, err = fa2.Remote(t.Context(), f2.TokenString(), f2.Keys().Writable)
	require.NoError(t, err)
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/x", nil)
	require.NoError(t, err)
	resp, err := remote.Transport.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, []string{"Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+f2.TokenString()))}, seen)
}

// A clone address that is not usable as a git remote is refused, never
// repaired.
func TestRemoteRefusesABadAddress(t *testing.T) {
	for _, fault := range []fake.Fault{fake.FaultHTTPSAddressHasNoUsername, fake.FaultCloneAddressCarriesTheCredential} {
		sh := fake.GitHubLike
		sh.Faults = []fake.Fault{fault}
		f, fa := fakeForges(t, sh)
		_, err := fa.Remote(t.Context(), f.TokenString(), f.Keys().Writable)
		require.Error(t, err, string(fault))
	}
}
