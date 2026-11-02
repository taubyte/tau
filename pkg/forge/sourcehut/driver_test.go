package sourcehut_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/sourcehut"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIforgetestpublichalf comment"

var ctx = context.Background()

type rig struct {
	f   *fixture
	reg *forge.Registry
}

func newRig(t *testing.T, opts ...sourcehut.Option) *rig {
	t.Helper()
	f := newFixture(t)
	opts = append([]sourcehut.Option{sourcehut.Transport(f.Transport()), sourcehut.PayloadKey(f.PublicKey())}, opts...)
	drv, err := sourcehut.New(f.Host(), opts...)
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)
	return &rig{f: f, reg: reg}
}

func (r *rig) sess(t *testing.T, full string) (*forge.Session, repokey.Key) {
	t.Helper()
	k := r.f.key(t, full)
	s, err := r.reg.For(k, forge.Token(goodToken))
	require.NoError(t, err)
	return s, k
}

func (r *rig) raw(op, body string) {
	r.f.mu.Lock()
	r.f.raw[op] = body
	r.f.mu.Unlock()
}

func (r *rig) unraw(op string) {
	r.f.mu.Lock()
	delete(r.f.raw, op)
	r.f.mu.Unlock()
}

func (r *rig) set(fn func(f *fixture)) {
	r.f.mu.Lock()
	fn(r.f)
	r.f.mu.Unlock()
}

func repoBody(field, owner, name string) string {
	return `{"data":{` + field + `{"id":7,"rid":"rid-7","name":"` + name + `","visibility":"PUBLIC","access":"RO","owner":{"canonicalName":"` + owner + `"},"HEAD":null}}}`
}

func TestNewRefusesWhatIsNotADeployment(t *testing.T) {
	type testCase struct {
		host string
		opts []sourcehut.Option
	}
	for name, tc := range map[string]testCase{
		"no host":           {host: ""},
		"a host spelled up": {host: "Git.Example.org"},
		"no transport":      {host: "git.example.org", opts: []sourcehut.Option{sourcehut.Transport(nil)}},
		"a key not base64":  {host: "git.example.org", opts: []sourcehut.Option{sourcehut.PayloadKey("!!")}},
		"a key too short":   {host: "git.example.org", opts: []sourcehut.Option{sourcehut.PayloadKey("AAAA")}},
		"an empty ssh user": {host: "git.example.org", opts: []sourcehut.Option{sourcehut.SSHUser("")}},
		"an ssh user@host":  {host: "git.example.org", opts: []sourcehut.Option{sourcehut.SSHUser("git@x")}},
	} {
		_, err := sourcehut.New(tc.host, tc.opts...)
		assert.Error(t, err, name)
	}
}

func TestSrHtSignsWithItsPublishedKey(t *testing.T) {
	drv, err := sourcehut.New(sourcehut.PublicHost)
	require.NoError(t, err)
	pk, ok := drv.(forge.PayloadKey)
	require.True(t, ok)
	assert.Equal(t, "uX7KWyyDNMaBma4aVbJ/cbUQpdjqczuCyK/HxzV/u+4=", b64(pk.PayloadKey()))
	assert.Equal(t, forge.SchemePayloadEd25519, drv.(forge.Signatures).Scheme())

	other, err := sourcehut.New("git.example.org")
	require.NoError(t, err)
	_, ok = other.(forge.Signatures)
	assert.False(t, ok)
}

func TestAddressableNamesOneTildeOwnerAndOneName(t *testing.T) {
	drv, err := sourcehut.New("git.example.org")
	require.NoError(t, err)
	assert.Error(t, drv.Addressable(repokey.Key{Host: "elsewhere.org", Owner: "~a", Name: "b"}))
	assert.Error(t, drv.Addressable(repokey.Key{Host: "git.example.org", Owner: "acme", Name: "b"}))
	assert.Error(t, drv.Addressable(repokey.Key{Host: "git.example.org", Owner: "~", Name: "b"}))
	assert.NoError(t, drv.Addressable(repokey.Key{Host: "git.example.org", Owner: "~acme", Name: "b"}))
}

func TestCloneAddressesAreTheInstances(t *testing.T) {
	r := newRig(t, sourcehut.SSHUser("srht"))
	s, k := r.sess(t, "~tau/pay")
	ssh, err := s.CloneAddress(k, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, "ssh://srht@"+r.f.Host()+"/~tau/pay", ssh.URL)
	assert.Equal(t, "srht", ssh.SSHAccount)
	_, err = s.CloneAddress(k, forge.Protocol(9))
	assert.Error(t, err)

	plain, err := sourcehut.New("127.0.0.1:8080", sourcehut.Plaintext())
	require.NoError(t, err)
	c, err := plain.Bind(forge.Token("t"))
	require.NoError(t, err)
	https, err := c.(forge.CloneAddresses).CloneAddress(repokey.Key{Host: "127.0.0.1:8080", Owner: "~a", Name: "b"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080/~a/b", https.URL)
}

func TestAccessReadsTheGrantAndTheOwner(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~acme/pay")
	lvl, err := s.Access(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, forge.AccessWrite, lvl)

	r.raw("Access", `{"data":{"me":null,"user":{"repository":{"id":7,"rid":"r","name":"pay","visibility":"PUBLIC","access":"NONE","owner":{"canonicalName":"~acme"}}}}}`)
	lvl, err = s.Access(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, forge.AccessNone, lvl)

	s, k = r.sess(t, "~tau/billing")
	r.unraw("Access")
	_, err = s.Access(ctx, k)
	assert.ErrorContains(t, err, "answered about")

	r.raw("Access", `{"data":{"me":null,"user":{"repository":{"name":"pay","owner":{"canonicalName":"acme"}}}}}`)
	_, err = s.Access(ctx, k)
	assert.ErrorContains(t, err, "no ~owner")
}

func TestAnswersThatAreNotAnswers(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/pay")
	type testCase struct{ op, body, want string }
	for name, tc := range map[string]testCase{
		"no user":                 {"Me", `{"data":{"me":null}}`, "belongs to nobody"},
		"no data":                 {"Me", `{"data":null}`, "answered no data"},
		"not json":                {"Me", `garbage`, "cannot read"},
		"data of the wrong shape": {"Me", `{"data":[1]}`, "cannot read"},
		"an error":                {"Me", `{"errors":[{"message":"` + strings.Repeat("x", 400) + `"}]}`, "…"},
	} {
		r.raw(tc.op, tc.body)
		_, err := s.Whoami(ctx)
		assert.ErrorContains(t, err, tc.want, name)
		r.unraw(tc.op)
	}

	for name, body := range map[string]string{
		"a name with a slash":  repoBody(`"user":{"repository":`, "~tau", "a/b"),
		"an owner with a path": repoBody(`"user":{"repository":`, "~tau/x", "pay"),
		"an implausible name":  repoBody(`"user":{"repository":`, "~tau", strings.Repeat("n", 300)),
		"an unparseable name":  repoBody(`"user":{"repository":`, "~tau", ".."),
		"a name with a space":  repoBody(`"user":{"repository":`, "~t au", "x"),
	} {
		r.raw("Repository", strings.Replace(body, `}}}`, `}}}}`, 1))
		_, err := s.Repository(ctx, k)
		assert.Error(t, err, name)
	}
}

func TestACredentialRefusedWithA403IsACredentialError(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/pay")
	r.f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	_, err := s.Repository(ctx, k)
	var ce *forge.CredentialError
	assert.ErrorAs(t, err, &ce)
}

func TestCreateAndDeleteCheckWhatTheForgeAnswered(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/fresh")

	r.raw("Me", `{"data":{"me":null}}`)
	_, err := s.Create(ctx, k, forge.CreateOptions{})
	assert.Error(t, err)
	r.unraw("Me")

	r.raw("Create", `{"data":{"createRepository":null}}`)
	_, err = s.Create(ctx, k, forge.CreateOptions{})
	assert.ErrorContains(t, err, "no repository")
	r.raw("Create", `{"data":{"createRepository":{"name":"other","owner":{"canonicalName":"~tau"}}}}`)
	_, err = s.Create(ctx, k, forge.CreateOptions{})
	assert.ErrorContains(t, err, "created")
	r.raw("Create", `{"data":{"createRepository":{"name":"x/y","owner":{"canonicalName":"~tau"}}}}`)
	_, err = s.Create(ctx, k, forge.CreateOptions{})
	assert.Error(t, err)
	r.unraw("Create")
	repo, err := s.Create(ctx, k, forge.CreateOptions{})
	require.NoError(t, err)
	assert.False(t, repo.Private)

	d, dk := r.sess(t, "~tau/scratch")
	for name, tc := range map[string]string{
		"null":          `{"data":{"deleteRepository":null}}`,
		"another":       `{"data":{"deleteRepository":{"name":"site","owner":{"canonicalName":"~tau"}}}}`,
		"unreadable":    `{"data":{"deleteRepository":{"name":"","owner":{"canonicalName":"~tau"}}}}`,
		"a graphql err": `{"errors":[{"message":"Access denied"}]}`,
	} {
		r.raw("Delete", tc)
		assert.Error(t, d.Delete(ctx, dk), name)
	}
	r.unraw("Delete")

	// A write is aimed by the answer to a lookup, so a lookup answering about
	// another repository aims nothing.
	m, mk := r.sess(t, "~tau/billing")
	assert.ErrorContains(t, m.Delete(ctx, mk), "nobody named")
}

func TestADeployKeyIsReadOnlyAndOnTheRepositoryAsked(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/site")

	_, err := s.InstallDeployKey(ctx, k, "t", "onlyone")
	assert.ErrorContains(t, err, "type and a blob")
	_, err = s.InstallDeployKey(ctx, k, "two words", pub)
	assert.ErrorContains(t, err, "one word")
	_, err = s.InstallDeployKey(ctx, r.f.key(t, "~tau/nope"), "t", pub)
	var nf *forge.NotFoundError
	assert.ErrorAs(t, err, &nf)

	r.set(func(f *fixture) { f.keyAccess = "RW" })
	_, err = s.InstallDeployKey(ctx, k, "t", pub)
	assert.ErrorContains(t, err, "that can push; it has been removed")
	r.set(func(f *fixture) { f.keyAccess, f.keyRepo = "", "elsewhere" })
	_, err = s.InstallDeployKey(ctx, k, "t", pub)
	assert.ErrorContains(t, err, "other than the one asked about; it has been removed")
	r.set(func(f *fixture) { f.gqlErrors["RemoveKey"] = "nope" })
	_, err = s.InstallDeployKey(ctx, k, "t", pub)
	assert.ErrorContains(t, err, "could not be removed again")
	r.set(func(f *fixture) { f.keyRepo = ""; delete(f.gqlErrors, "RemoveKey") })
	keys, err := s.ListDeployKeys(ctx, k)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "the one refused key whose removal failed is the one left, and the error said so")

	r.raw("InstallKey", `{"data":{"createDeployKey":{"rid":""}}}`)
	_, err = s.InstallDeployKey(ctx, k, "t", pub)
	assert.ErrorContains(t, err, "no id")
	r.raw("InstallKey", `{"errors":[{"message":"Access denied"}]}`)
	_, err = s.InstallDeployKey(ctx, k, "t", pub)
	assert.Error(t, err)
	r.unraw("InstallKey")

	h, err := s.InstallDeployKey(ctx, k, "t", pub)
	require.NoError(t, err)
	r.raw("RemoveKey", `{"data":{"deleteDeployKey":null}}`)
	assert.ErrorContains(t, s.RemoveDeployKey(ctx, k, h), "did not say")
	r.raw("RemoveKey", `{"errors":[{"message":"no"}]}`)
	assert.Error(t, s.RemoveDeployKey(ctx, k, h))
	r.unraw("RemoveKey")
	assert.Error(t, s.RemoveDeployKey(ctx, k, forge.Handle(strings.Repeat("h", 300))))

	// A handle that is another repository's is not removed from this one.
	other, ok := r.sess(t, "~tau/scratch")
	_, err = other.ListDeployKeys(ctx, ok)
	require.NoError(t, err)
	assert.ErrorContains(t, other.RemoveDeployKey(ctx, ok, h), "has no deploy key")
	assert.True(t, r.f.DeployKeyInstalled(h))
}

func TestDeployKeyListingsAreChecked(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/site")
	for name, body := range map[string]string{
		"null repository":   `{"data":{"user":{"repository":null}}}`,
		"another":           `{"data":{"user":{"repository":{"name":"pay","owner":{"canonicalName":"~tau"},"deployKeys":{"results":[]}}}}}`,
		"unreadable":        `{"data":{"user":{"repository":{"name":"","owner":{"canonicalName":"~tau"},"deployKeys":{"results":[]}}}}}`,
		"a key with no rid": `{"data":{"user":{"repository":{"name":"site","owner":{"canonicalName":"~tau"},"deployKeys":{"results":[{"rid":""}]}}}}}`,
		"an error":          `{"errors":[{"message":"no"}]}`,
	} {
		r.raw("Keys", body)
		_, err := s.ListDeployKeys(ctx, k)
		assert.Error(t, err, name)
	}
	r.unraw("Keys")
	assert.Error(t, s.RemoveDeployKey(ctx, k, "key-1"), "a listing that fails removes nothing")
}

func TestPagersStop(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/site")
	r.set(func(f *fixture) { f.pageRepeat = true })
	assert.ErrorContains(t, s.ListRepositories(ctx, func(forge.Repository) bool { return true }), "already answered")
	r.set(func(f *fixture) { f.pageRepeat, f.pageForever = false, true })
	_, err := s.ListDeployKeys(ctx, k)
	assert.ErrorContains(t, err, "after 1000 pages")
	_, err = s.ListPushHooks(ctx, k)
	assert.ErrorContains(t, err, "after 1000 pages")
	r.set(func(f *fixture) { f.pageForever = false })

	r.raw("List", `{"data":{"me":null}}`)
	assert.ErrorContains(t, s.ListRepositories(ctx, func(forge.Repository) bool { return true }), "no user")
	r.raw("List", `{"data":{"me":{"repositories":{"results":[{"name":"","owner":{"canonicalName":"~tau"}},{"name":"ok","owner":{"canonicalName":"~tau"},"HEAD":{"name":"refs/heads/trunk"}}]}}}}`)
	var seen []forge.Repository
	require.NoError(t, s.ListRepositories(ctx, func(repo forge.Repository) bool { seen = append(seen, repo); return true }))
	require.Len(t, seen, 1, "an unreadable entry is skipped, not yielded")
	assert.Equal(t, "trunk", seen[0].DefaultBranch)
}

func TestHooksAreTheRepositorysOwn(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/site")
	cb, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)

	_, err = s.RegisterPushHook(ctx, r.f.key(t, "~tau/nope"), cb, secret)
	assert.Error(t, err)
	r.raw("RegisterHook", `{"data":{"createGitWebhook":{"id":0}}}`)
	_, err = s.RegisterPushHook(ctx, k, cb, secret)
	assert.ErrorContains(t, err, "no id")
	r.raw("RegisterHook", `{"errors":[{"message":"no"}]}`)
	_, err = s.RegisterPushHook(ctx, k, cb, secret)
	assert.Error(t, err)
	r.unraw("RegisterHook")

	hook, err := s.RegisterPushHook(ctx, k, cb, secret)
	require.NoError(t, err)

	assert.ErrorContains(t, s.RemovePushHook(ctx, k, "x1"), "not a webhook id")
	assert.ErrorContains(t, s.RemovePushHook(ctx, k, "-4"), "not a webhook id")
	other, ok := r.sess(t, "~tau/scratch")
	assert.ErrorContains(t, other.RemovePushHook(ctx, ok, hook.Handle), "has no webhook")
	assert.Error(t, s.RemovePushHook(ctx, r.f.key(t, "~tau/nope"), hook.Handle))

	r.raw("RemoveHook", `{"data":{"deleteGitWebhook":{"id":1}}}`)
	assert.ErrorContains(t, s.RemovePushHook(ctx, k, hook.Handle), "did not say")
	r.raw("RemoveHook", `{"errors":[{"message":"no"}]}`)
	assert.Error(t, s.RemovePushHook(ctx, k, hook.Handle))
	r.unraw("RemoveHook")

	r.raw("Hooks", `{"data":{"gitWebhooks":{"results":[{"id":0}]}}}`)
	_, err = s.ListPushHooks(ctx, k)
	assert.ErrorContains(t, err, "no id")
	r.raw("Hooks", `{"errors":[{"message":"no"}]}`)
	_, err = s.ListPushHooks(ctx, k)
	assert.Error(t, err)
	r.unraw("Hooks")
	require.NoError(t, s.RemovePushHook(ctx, k, hook.Handle))

	// The wire string is written into a document the forge runs, so only the
	// shape the scheme derives is accepted.
	c, err := r.reg.Driver(r.f.Host())
	require.NoError(t, err)
	cl, err := c.Bind(forge.Token(goodToken))
	require.NoError(t, err)
	_, err = cl.(forge.PushHooks).RegisterPushHook(ctx, k, cb, "tau_00\n} mutation {")
	assert.ErrorContains(t, err, "not the GraphQL alias")
}

func TestPushReadsOneRefOfOnePostReceive(t *testing.T) {
	r := newRig(t)
	s, k := r.sess(t, "~tau/site")
	cb, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook, err := s.RegisterPushHook(ctx, k, cb, secret)
	require.NoError(t, err)
	wire, err := forge.SchemePayloadEd25519.Wire(secret)
	require.NoError(t, err)

	deliver := func(webhook any) (forge.Push, error) {
		body, err := json.Marshal(map[string]any{"data": map[string]any{"webhook": webhook, wire: "Query"}})
		require.NoError(t, err)
		return r.reg.VerifyPush(hook, forge.Delivery{Header: forge.SignPayload(r.f.priv, body, "n"), Body: body})
	}
	repo := map[string]any{"name": "site", "owner": map[string]any{"canonicalName": "~tau"}}
	upd := func(ref, old, nw any) map[string]any { return map[string]any{"ref": ref, "old": old, "new": nw} }
	ref := map[string]any{"name": "refs/heads/main"}
	id := func(s string) map[string]any { return map[string]any{"id": s} }
	tip := strings.Repeat("b", 40)

	_, err = deliver(5)
	assert.ErrorContains(t, err, "unreadable")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE"})
	assert.ErrorContains(t, err, "names no repository")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": map[string]any{"name": "site", "owner": map[string]any{"canonicalName": "tau"}}})
	assert.ErrorContains(t, err, "can address")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{}})
	assert.ErrorIs(t, err, forge.ErrNotAPush)
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{upd(ref, nil, id(tip)), upd(ref, nil, id(tip))}})
	assert.ErrorContains(t, err, "updated 2 refs")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{upd(nil, nil, id(tip))}})
	assert.ErrorContains(t, err, "names no ref")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{upd(ref, nil, nil)}})
	var ve *forge.VerificationError
	assert.ErrorAs(t, err, &ve, "a ref with neither tip names no commit")
	_, err = deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{upd(ref, id(strings.Repeat("c", 64)), nil)}})
	assert.ErrorIs(t, err, forge.ErrRefDeleted, "a deletion is read at the repository's hash width")
	push, err := deliver(map[string]any{"event": "GIT_POST_RECEIVE", "repository": repo, "updates": []any{upd(ref, nil, id(tip))}})
	require.NoError(t, err)
	assert.Equal(t, forge.Push{Key: k, Ref: "refs/heads/main", Commit: tip}, push)
	assert.False(t, errors.Is(err, forge.ErrNotAPush))
}
