package azure_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/azure"
	"github.com/taubyte/tau/pkg/git/repokey"
)

var ctx = context.Background()

func registry(t *testing.T, f *fixture, opts ...azure.Option) *forge.Registry {
	t.Helper()
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: newDriver(t, f, opts...)})
	require.NoError(t, err)
	return reg
}

func session(t *testing.T, f *fixture, path string, tok string) (*forge.Session, repokey.Key) {
	t.Helper()
	k := f.key(t, path)
	s, err := registry(t, f).For(k, forge.Token(tok))
	require.NoError(t, err)
	return s, k
}

// client binds the driver directly, past the session's guards, so the
// driver's own refusals are what is under test.
func client(t *testing.T, drv forge.Driver) forge.Client {
	t.Helper()
	c, err := drv.Bind(forge.Token(goodToken))
	require.NoError(t, err)
	return c
}

func TestNewRefusesAnIncompleteDeployment(t *testing.T) {
	for name, tc := range map[string]struct {
		host string
		opts []azure.Option
	}{
		"no host":              {"", []azure.Option{azure.Organizations(org)}},
		"not a host spelling":  {"Dev.Azure.com", []azure.Option{azure.Organizations(org)}},
		"no organization":      {"dev.azure.com", nil},
		"an empty one":         {"dev.azure.com", []azure.Option{azure.Organizations("")}},
		"a nested one":         {"dev.azure.com", []azure.Option{azure.Organizations("a/b")}},
		"a dot segment":        {"dev.azure.com", []azure.Option{azure.Organizations("..")}},
		"a nil transport":      {"dev.azure.com", []azure.Option{azure.Organizations(org), azure.Transport(nil)}},
		"organizations absent": {"dev.azure.com", []azure.Option{azure.Bearer()}},
	} {
		_, err := azure.New(tc.host, tc.opts...)
		assert.Error(t, err, name)
	}
}

// Azure DevOps cannot sign a delivery, so the hook faces are absent and asking
// for one is a refusal by name rather than a hook tau could not verify.
func TestPushHooksAreUnsupported(t *testing.T) {
	f := newFixture(t)
	s, k := session(t, f, "acme/site/_git/site", goodToken)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	cb, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	var un *forge.UnsupportedError
	_, err = s.RegisterPushHook(ctx, k, cb, secret)
	require.ErrorAs(t, err, &un)
	assert.Equal(t, forge.CapPushHooks, un.Capability)
	_, err = s.ListPushHooks(ctx, k)
	assert.ErrorAs(t, err, &un)
	assert.ErrorAs(t, s.RemovePushHook(ctx, k, "h"), &un)
	assert.False(t, s.Capabilities().Has(forge.CapSignatures))
	assert.Empty(t, f.Requests(), "an unsupported face went to the wire")
}

func TestAddressableIsTheOneShape(t *testing.T) {
	drv, err := azure.New("dev.azure.com", azure.Organizations("Acme", "acme", "other"))
	require.NoError(t, err)
	parse := func(s string) repokey.Key {
		k, err := repokey.Parse(s)
		require.NoError(t, err)
		return k
	}
	require.NoError(t, drv.Addressable(parse("https://dev.azure.com/acme/pay/_git/pay")))
	require.NoError(t, drv.Addressable(parse("https://dev.azure.com/other/pay/_git/pay")))
	for _, bad := range []string{
		"https://gitlab.com/acme/pay/_git/pay",        // another host
		"https://dev.azure.com/stranger/pay/_git/pay", // an organization nobody named
		"https://dev.azure.com/acme/pay/pay",          // no _git
		"https://dev.azure.com/tfs/acme/pay/_git/pay", // a virtual directory
		"https://dev.azure.com/acme/_git/pay",         // no project
	} {
		assert.Error(t, drv.Addressable(parse(bad)), bad)
	}
	_, err = drv.Bind(forge.Credential{})
	assert.ErrorIs(t, err, forge.ErrNoCredential)
}

func TestAPatTravelsAsBasicAndAnEntraTokenAsBearer(t *testing.T) {
	f := newFixture(t)
	s, _ := session(t, f, "acme/pay/_git/pay", goodToken)
	_, err := s.Whoami(ctx)
	require.NoError(t, err)
	reqs := f.Requests()
	require.NotEmpty(t, reqs)
	assert.True(t, strings.HasPrefix(reqs[0].Header.Get("Authorization"), "Basic "))
	assert.Contains(t, reqs[0].URL, "api-version=7.1-preview.1")

	reg := registry(t, f, azure.Bearer())
	sb, err := reg.Host(f.Host(), forge.Token(goodToken))
	require.NoError(t, err)
	_, err = sb.Whoami(ctx)
	var ce *forge.CredentialError
	require.ErrorAs(t, err, &ce, "the fixture takes only a PAT, so a bearer token is a refused credential")
	assert.Equal(t, "Bearer "+goodToken, f.Requests()[0].Header.Get("Authorization"))
}

func TestWhoami(t *testing.T) {
	f := newFixture(t)
	s, _ := session(t, f, "acme/pay/_git/pay", goodToken)
	id, err := s.Whoami(ctx)
	require.NoError(t, err)
	assert.Equal(t, forge.Identity{Login: account, Subject: userID, Email: account}, id)

	f.noAccount = true
	_, err = s.Whoami(ctx)
	assert.ErrorContains(t, err, "belongs to nobody")
}

// TestA203IsARefusedCredential: Azure DevOps answers a credential it will not
// take with 203 and a sign-in page. Read as success, a deletion "succeeds".
func TestA203IsARefusedCredential(t *testing.T) {
	f := newFixture(t)
	s, k := session(t, f, "acme/scratch/_git/scratch", sickToken)
	var ce *forge.CredentialError
	_, err := s.Whoami(ctx)
	require.ErrorAs(t, err, &ce)
	lvl, err := s.Access(ctx, k)
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, forge.AccessNone, lvl)
	require.ErrorAs(t, s.Delete(ctx, k), &ce)
	_, ok := f.repos["scratch/scratch"]
	assert.True(t, ok)
}

func TestARepositoryAnswerIsChecked(t *testing.T) {
	f := newFixture(t)
	s, k := session(t, f, "acme/pay/_git/pay", goodToken)
	good := f.repoBody(f.repos["pay/pay"])
	with := func(mut func(map[string]any)) map[string]any {
		b := map[string]any{}
		raw, err := json.Marshal(good)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &b))
		mut(b)
		return b
	}
	for name, body := range map[string]map[string]any{
		"no remote":        with(func(b map[string]any) { delete(b, "remoteUrl") }),
		"a huge remote":    with(func(b map[string]any) { b["remoteUrl"] = "https://" + f.host + "/" + strings.Repeat("a", 300) }),
		"no ids":           with(func(b map[string]any) { delete(b, "id") }),
		"not a URL":        with(func(b map[string]any) { b["remoteUrl"] = "https://[::1" }),
		"no key":           with(func(b map[string]any) { b["remoteUrl"] = "https://" + f.host + "/" }),
		"another host":     with(func(b map[string]any) { b["remoteUrl"] = "https://elsewhere.invalid/acme/pay/_git/pay" }),
		"an unaddressable": with(func(b map[string]any) { b["remoteUrl"] = "https://" + f.host + "/acme/pay/pay" }),
	} {
		f.answerAs = body
		_, err := s.Repository(ctx, k)
		assert.Error(t, err, name)
		assert.NotContains(t, err.Error(), "elsewhere.invalid/acme", name)
	}

	repo, err := s.Repository(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.True(t, repo.Private)
	assert.Equal(t, "https://"+f.host+"/acme/pay/_git/pay", repo.HTTPS, "the organization in the user half is dropped")
}

func TestAccessIsMatchedToItsQuestion(t *testing.T) {
	f := newFixture(t)
	f.seed("pay", "dark", 0)
	s, k := session(t, f, "acme/pay/_git/pay", goodToken)

	f.evalDrop = true
	_, err := s.Access(ctx, k)
	assert.ErrorContains(t, err, "did not answer", "a short answer must not read as a denial")
	f.evalDrop = false

	f.evalForeign = true
	_, err = s.Access(ctx, k)
	assert.Error(t, err, "an answer about another token is not an answer")
	f.evalForeign = false

	lvl, err := s.Access(ctx, f.key(t, "acme/pay/_git/dark"))
	require.NoError(t, err)
	assert.Equal(t, forge.AccessNone, lvl)

	lvl, err = s.Access(ctx, f.key(t, "acme/pay/_git/billing"))
	assert.Error(t, err, "a verdict about a renamed repository is not a verdict about this key")
	assert.Equal(t, forge.AccessNone, lvl)

	f.Break()
	_, err = s.Access(ctx, k)
	f.Unbreak()
	assert.Error(t, err)
}

func TestTheListingSkipsWhatItCannotName(t *testing.T) {
	f := newFixture(t)
	f.listExtra = []map[string]any{{"id": "x", "remoteUrl": "https://elsewhere.invalid/a/b/_git/c", "project": map[string]any{"id": "p"}}}
	s, _ := session(t, f, "acme/pay/_git/pay", goodToken)
	var seen []string
	require.NoError(t, s.ListRepositories(ctx, func(r forge.Repository) bool {
		seen = append(seen, r.Key.Name)
		return true
	}))
	assert.ElementsMatch(t, []string{"pay", "public", "site", "scratch"}, seen, "the disabled and the foreign are skipped")
}

// session2 is a host session for tok.
func session2(t *testing.T, f *fixture, tok string) *forge.Session {
	t.Helper()
	s, err := registry(t, f).Host(f.Host(), forge.Token(tok))
	require.NoError(t, err)
	return s
}

func TestAListingWithARefusedCredentialSaysSo(t *testing.T) {
	f := newFixture(t)
	var ce *forge.CredentialError
	require.ErrorAs(t, session2(t, f, badToken).ListRepositories(ctx, func(forge.Repository) bool { return true }), &ce)
}

func TestCreateKeepsTheProjectsVisibility(t *testing.T) {
	f := newFixture(t)
	s, _ := session(t, f, "acme/pay/_git/pay", goodToken)

	_, err := s.Create(ctx, f.key(t, "acme/pay/_git/open"), forge.CreateOptions{Private: false})
	assert.ErrorContains(t, err, "visibility is the project's")
	_, err = s.Create(ctx, f.key(t, "acme/public/_git/shut"), forge.CreateOptions{Private: true})
	assert.ErrorContains(t, err, "visibility is the project's")
	repo, err := s.Create(ctx, f.key(t, "acme/public/_git/open"), forge.CreateOptions{Private: false})
	require.NoError(t, err)
	assert.False(t, repo.Private)

	_, err = s.Create(ctx, f.key(t, "acme/ghost/_git/x"), forge.CreateOptions{Private: true})
	assert.Error(t, err, "a project that does not exist")

	f.createAnswer = f.repoBody(f.repos["pay/pay"])
	_, err = s.Create(ctx, f.key(t, "acme/pay/_git/other"), forge.CreateOptions{Private: true})
	assert.ErrorContains(t, err, "the forge created")
	f.createAnswer = nil

	f.emptyCreate = true
	_, err = s.Create(ctx, f.key(t, "acme/pay/_git/empty"), forge.CreateOptions{Private: true})
	assert.Error(t, err)
}

func TestCreateRefusesAProjectWithNoID(t *testing.T) {
	f := newFixture(t)
	f.projects["pay"].id = ""
	s, _ := session(t, f, "acme/site/_git/site", goodToken)
	_, err := s.Create(ctx, f.key(t, "acme/pay/_git/x"), forge.CreateOptions{Private: true})
	assert.ErrorContains(t, err, "no id")
}

func TestDeleteThroughARenameIsRefused(t *testing.T) {
	f := newFixture(t)
	f.repos["pay/pay"].bits |= permAdmin
	s, k := session(t, f, "acme/pay/_git/billing", goodToken)
	assert.Error(t, s.Delete(ctx, k))
	_, ok := f.repos["pay/pay"]
	assert.True(t, ok, "the repository the old name now answers with was deleted")
	var nf *forge.NotFoundError
	assert.ErrorAs(t, s.Delete(ctx, f.key(t, "acme/pay/_git/nope")), &nf)
}

func TestCloneAddresses(t *testing.T) {
	drv, err := azure.New("dev.azure.com", azure.Organizations(org))
	require.NoError(t, err)
	c := client(t, drv).(forge.CloneAddresses)
	k, err := repokey.Parse("https://dev.azure.com/acme/pay/_git/pay")
	require.NoError(t, err)

	a, err := c.CloneAddress(k, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "https://dev.azure.com/acme/pay/_git/pay", HTTPUsername: "pat"}, a)

	a, err = c.CloneAddress(k, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "ssh://git@ssh.dev.azure.com/v3/acme/pay/pay", SSHAccount: "git"}, a)

	// Both addresses, and the scp form Azure itself prints, are ONE repository
	// through the registry, though repokey alone reads the ssh one as another.
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "dev.azure.com", Driver: drv})
	require.NoError(t, err)
	for _, spelling := range []string{a.URL, "git@ssh.dev.azure.com:v3/Acme/Pay/Pay", "https://acme@dev.azure.com/acme/pay/_git/pay"} {
		got, err := reg.Key(spelling)
		require.NoError(t, err)
		assert.Equal(t, k, got, spelling)
	}
	raw, err := repokey.Parse(a.URL)
	require.NoError(t, err)
	assert.NotEqual(t, k, raw, "repokey has no vocabulary; the forge's spelling is the registry's to apply")
	// A path on the ssh host that is not the v3 shape is not rewritten.
	got, err := reg.Key("ssh://git@ssh.dev.azure.com/acme/pay/pay")
	require.NoError(t, err)
	assert.Equal(t, "ssh.dev.azure.com", got.Host)

	server, err := azure.New("tfs.example.com", azure.Organizations("defaultcollection"))
	require.NoError(t, err)
	sk, err := repokey.Parse("https://tfs.example.com/DefaultCollection/Pay/_git/Pay")
	require.NoError(t, err)
	a, err = client(t, server).(forge.CloneAddresses).CloneAddress(sk, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "ssh://git@tfs.example.com/defaultcollection/pay/_git/pay", SSHAccount: "git"}, a)
	sreg, err := forge.New(forge.Policy{}, forge.Binding{Host: "tfs.example.com", Driver: server})
	require.NoError(t, err)
	got, err = sreg.Key(a.URL)
	require.NoError(t, err)
	assert.Equal(t, sk, got, "a Server's ssh address is already its https key")

	other, err := repokey.Parse("https://dev.azure.com/acme/pay/pay")
	require.NoError(t, err)
	_, err = c.CloneAddress(other, forge.HTTPS)
	assert.Error(t, err)
	dot, err := repokey.Parse("https://dev.azure.com/acme/pay/_git/pay")
	require.NoError(t, err)
	dot.Name = "."
	_, err = c.CloneAddress(dot, forge.HTTPS)
	assert.Error(t, err)

	// The client refuses a key its session would have refused, too.
	_, err = client(t, drv).Repository(ctx, other)
	assert.Error(t, err)
	_, err = client(t, drv).(forge.Creator).Create(ctx, other, forge.CreateOptions{})
	assert.Error(t, err)
	assert.Equal(t, "dev.azure.com", client(t, drv).Host())
}
