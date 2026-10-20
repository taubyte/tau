package forgeauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

const deployKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl tau"

// What a link installs is installed here, through the registry, under the
// caller's own forge credential; inspecting first says what a link needs to
// know before it claims anything.
func TestInspectThenInstallThenReplace(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	fa, err := New(reg)
	require.NoError(t, err)
	ctx := t.Context()
	callback, _ := url.Parse("http://cloud.example/hook/proj")
	key := f.Keys().Admin

	in, err := fa.Inspect(ctx, f.TokenString(), key, callback)
	require.NoError(t, err)
	require.Equal(t, frontend.Inspection{Repository: key.String(), Admin: true}, in)

	install := frontend.Install{Callback: callback, Secret: []byte("0123456789abcdef0123456789abcdef"), Title: "tau http://cloud.example", DeployKey: deployKey}
	require.NoError(t, fa.Install(ctx, f.TokenString(), key, install))
	require.NotEmpty(t, f.LastHookWire())
	require.True(t, f.LastDeployKeyReadOnly())

	in, err = fa.Inspect(ctx, f.TokenString(), key, callback)
	require.NoError(t, err)
	require.True(t, in.Hooked)

	// A second install beside the first is refused; a replacement is not.
	require.ErrorIs(t, fa.Install(ctx, f.TokenString(), key, install), frontend.ErrAlreadyHooked)
	install.Replace = true
	require.NoError(t, fa.Install(ctx, f.TokenString(), key, install))
	s, err := reg.For(key, f.Token())
	require.NoError(t, err)
	hooks, err := s.ListPushHooks(ctx, key)
	require.NoError(t, err)
	require.Len(t, hooks, 1, "the old hook was deleted")
	keys, err := s.ListDeployKeys(ctx, key)
	require.NoError(t, err)
	require.Len(t, keys, 1, "the old deploy key was deleted")
}

// A repository the credential only writes to is said to be one it may not
// install on, and installing on it is refused by name.
func TestInstallTakesAdmin(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	fa, err := New(reg)
	require.NoError(t, err)
	callback, _ := url.Parse("http://cloud.example/hook/proj")

	in, err := fa.Inspect(t.Context(), f.TokenString(), f.Keys().Writable, callback)
	require.NoError(t, err)
	require.False(t, in.Admin)
	require.False(t, in.Hooked)
	require.ErrorIs(t, fa.Install(t.Context(), f.TokenString(), f.Keys().Writable, frontend.Install{Callback: callback}), frontend.ErrNotAdmin)

	_, err = fa.Inspect(t.Context(), f.TokenString(), f.Keys().Missing, callback)
	require.Error(t, err)
	_, err = fa.Inspect(t.Context(), f.TokenString(), repokey.Key{Host: "unserved.example", Owner: "a", Name: "b"}, callback)
	require.Error(t, err)
	require.Error(t, fa.Install(t.Context(), f.TokenString(), repokey.Key{Host: "unserved.example", Owner: "a", Name: "b"}, frontend.Install{Callback: callback}))

	// A strict registry refuses a plain-http callback, since a delivery
	// carries a secret.
	strict, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	sa, err := New(strict)
	require.NoError(t, err)
	require.ErrorContains(t, sa.Install(t.Context(), f.TokenString(), f.Keys().Admin, frontend.Install{Callback: callback, Secret: []byte("0123456789abcdef0123456789abcdef"), DeployKey: deployKey}), "not https")
}

// stuck is a forge whose every step can be made to fail, the shapes the fake
// forge cannot take.
type stuck struct {
	callback, title string
	fail            string
	caps            bool
}

func (d *stuck) Host() string                  { return "stuck.example" }
func (d *stuck) Addressable(repokey.Key) error { return nil }
func (d *stuck) Bind(forge.Credential) (forge.Client, error) {
	if !d.caps {
		return bare{}, nil
	}
	return d, nil
}
func (d *stuck) Scheme() forge.Scheme { return forge.SchemeHubSHA256 }
func (d *stuck) Push(forge.VerifiedBody, http.Header) (forge.PushClaim, error) {
	return forge.PushClaim{}, forge.ErrNotAPush
}
func (d *stuck) err(step string) error {
	if d.fail == step {
		return errors.New("stuck " + step)
	}
	return nil
}
func (d *stuck) Whoami(context.Context) (forge.Identity, error) {
	return forge.Identity{Login: "me"}, nil
}
func (d *stuck) Access(context.Context, repokey.Key) (forge.Access, error) {
	return forge.AccessAdmin, d.err("access")
}
func (d *stuck) Repository(_ context.Context, k repokey.Key) (forge.Repository, error) {
	return forge.Repository{Key: k}, d.err("repository")
}
func (d *stuck) RegisterPushHook(context.Context, repokey.Key, *url.URL, string) (forge.Handle, error) {
	return "h-new", d.err("register")
}
func (d *stuck) RemovePushHook(context.Context, repokey.Key, forge.Handle) error {
	return d.err("remove-hook")
}
func (d *stuck) ListPushHooks(context.Context, repokey.Key) ([]forge.InstalledPushHook, error) {
	return []forge.InstalledPushHook{{Handle: "h-old", Callback: d.callback}, {Handle: "h-else", Callback: "https://else"}}, d.err("list-hooks")
}
func (d *stuck) InstallDeployKey(context.Context, repokey.Key, string, string) (forge.Handle, error) {
	return "k-new", d.err("install-key")
}
func (d *stuck) RemoveDeployKey(context.Context, repokey.Key, forge.Handle) error {
	return d.err("remove-key")
}
func (d *stuck) ListDeployKeys(context.Context, repokey.Key) ([]forge.InstalledDeployKey, error) {
	return []forge.InstalledDeployKey{{Handle: "k-other", Title: "someone else's"}, {Handle: "k-old", Title: d.title}}, d.err("list-keys")
}

// bare is a client that answers identity and access and nothing a link needs.
type bare struct{}

func (bare) Host() string { return "stuck.example" }
func (bare) Repository(_ context.Context, k repokey.Key) (forge.Repository, error) {
	return forge.Repository{Key: k}, nil
}

func (bare) Whoami(context.Context) (forge.Identity, error) { return forge.Identity{Login: "me"}, nil }
func (bare) Access(context.Context, repokey.Key) (forge.Access, error) {
	return forge.AccessAdmin, nil
}

func TestInstallStopsOnEveryStepThatFails(t *testing.T) {
	callback, _ := url.Parse("https://cloud.example/hook/proj")
	key := repokey.Key{Host: "stuck.example", Owner: "acme", Name: "cfg"}
	run := func(d *stuck) (*Forges, frontend.Install) {
		d.callback, d.title = callback.String(), "tau https://cloud.example"
		reg, err := forge.New(forge.Policy{}, forge.Binding{Host: d.Host(), Driver: d})
		require.NoError(t, err)
		fa, err := New(reg)
		require.NoError(t, err)
		return fa, frontend.Install{Callback: callback, Title: d.title, DeployKey: deployKey, Secret: []byte("0123456789abcdef0123456789abcdef"), Replace: true}
	}

	fa, in := run(&stuck{caps: true})
	require.NoError(t, fa.Install(t.Context(), "tok", key, in))
	got, err := fa.Inspect(t.Context(), "tok", key, callback)
	require.NoError(t, err)
	require.True(t, got.Hooked)

	for _, step := range []string{"access", "list-hooks", "remove-hook", "list-keys", "remove-key", "register", "install-key"} {
		fa, in := run(&stuck{caps: true, fail: step})
		require.ErrorContains(t, fa.Install(t.Context(), "tok", key, in), "stuck "+step, step)
	}
	for _, step := range []string{"repository", "access", "list-hooks"} {
		fa, _ := run(&stuck{caps: true, fail: step})
		_, err := fa.Inspect(t.Context(), "tok", key, callback)
		require.ErrorContains(t, err, "stuck "+step, step)
	}

	// A forge that installs neither is said to be one, so a link can say
	// --manual.
	fa, in = run(&stuck{})
	require.ErrorIs(t, fa.Install(t.Context(), "tok", key, in), frontend.ErrCannotInstall)
	_, err = fa.Inspect(t.Context(), "tok", key, callback)
	require.ErrorIs(t, err, frontend.ErrCannotInstall)
}
