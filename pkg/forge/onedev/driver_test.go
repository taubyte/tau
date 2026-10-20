package onedev_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/onedev"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// The conformance suite proves the contract; these are the answers a real
// OneDev rarely gives, each one a place a driver could turn a malformed reply
// into a verdict.

func session(t *testing.T, f *fixture, full string) (*forge.Session, repokey.Key) {
	t.Helper()
	return sessionOn(t, f, full), f.key(t, full)
}

func sessionOn(t *testing.T, f *fixture, full string) *forge.Session {
	t.Helper()
	s, err := f.registry(t, forge.Policy{AllowUnboundSchemes: true}).For(f.key(t, full), forge.Token(goodToken))
	require.NoError(t, err)
	return s
}

// answerOn answers every request whose method and path match with status and
// body, and leaves the rest to the fixture.
func answerOn(method, pathSuffix string, status int, body string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != method || !strings.HasSuffix(r.URL.Path, pathSuffix) {
			return false
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
		return true
	}
}

func TestNewRefusesWhatItCannotServe(t *testing.T) {
	_, err := onedev.New("")
	require.Error(t, err)
	_, err = onedev.New("OneDev.Example")
	require.Error(t, err, "a host repokey would never produce")
	_, err = onedev.New("onedev.example", onedev.Transport(nil))
	require.Error(t, err)

	drv, err := onedev.New("127.0.0.1:6610", onedev.Plaintext())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:6610", drv.Host())
	c, err := drv.Bind(forge.Token("t"))
	require.NoError(t, err)
	k, err := repokey.Parse("http://127.0.0.1:6610/acme/pay.git")
	require.NoError(t, err)
	addr, err := c.(forge.CloneAddresses).CloneAddress(k, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:6610/acme/pay.git", addr.URL, "Plaintext is what the clone address is spelled in")
}

func TestTheZeroPolicyRefusesThisDriver(t *testing.T) {
	drv, err := onedev.New("onedev.example")
	require.NoError(t, err)
	assert.Equal(t, forge.SchemeOneDevToken, drv.(forge.Signatures).Scheme())
	_, err = forge.New(forge.Policy{}, forge.Binding{Host: "onedev.example", Driver: drv})
	require.Error(t, err, "OneDev cannot sign, and a deployment must say it accepts that")
}

func TestAddressableRefusesWhatAKeyCannotName(t *testing.T) {
	drv, err := onedev.New("onedev.example")
	require.NoError(t, err)
	assert.Error(t, drv.Addressable(repokey.Key{Host: "other.example", Owner: "acme", Name: "pay"}))
	assert.Error(t, drv.Addressable(repokey.Key{Host: "onedev.example", Name: "root-project"}), "a root project has no owner half")
	assert.Error(t, drv.Addressable(repokey.Key{Host: "onedev.example", Owner: "acme/..", Name: "pay"}))
	assert.NoError(t, drv.Addressable(repokey.Key{Host: "onedev.example", Owner: "a/b/c", Name: "pay"}))
}

func TestWhoamiRefusesAnAnswerNamingNobody(t *testing.T) {
	f := newFixture(t)
	s := sessionOn(t, f, "acme/pay")
	f.Answer(answerOn(http.MethodGet, "/users/me", 200, `{}`))
	_, err := s.Whoami(t.Context())
	require.Error(t, err)
	f.Answer(answerOn(http.MethodGet, "/users/me", 200, `not json`))
	_, err = s.Whoami(t.Context())
	require.Error(t, err)
}

func TestAccess(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	t.Run("a project visible without its code is no access", func(t *testing.T) {
		s, k := session(t, f, "acme/issues")
		lvl, err := s.Access(ctx, k)
		require.NoError(t, err)
		assert.Equal(t, forge.AccessNone, lvl)
	})
	t.Run("a path resolving to a project named otherwise is not that project's grant", func(t *testing.T) {
		s, k := session(t, f, "acme/billing")
		_, err := s.Access(ctx, k)
		require.ErrorContains(t, err, "answered about")
	})
	for _, tc := range []struct {
		name, method, suffix string
		status               int
		cred                 bool
	}{
		{"settings answering 500", http.MethodGet, "/setting", 500, false},
		{"the advertisement answering 401", http.MethodGet, "/info/refs", 401, true},
		{"the default branch answering 500", http.MethodGet, "/default-branch", 500, false},
		{"the clone address answering 500", http.MethodGet, "/clone-url", 500, false},
	} {
		t.Run(tc.name+" is not a verdict", func(t *testing.T) {
			s, k := session(t, f, "acme/public")
			f.Answer(answerOn(tc.method, tc.suffix, tc.status, ""))
			defer f.Answer(nil)
			lvl, err := s.Access(ctx, k)
			require.Error(t, err)
			assert.Equal(t, forge.AccessNone, lvl)
			var ce *forge.CredentialError
			assert.Equal(t, tc.cred, errors.As(err, &ce))
		})
	}
	t.Run("an id that is no project is refused", func(t *testing.T) {
		s, k := session(t, f, "acme/pay")
		f.Answer(answerOn(http.MethodGet, "/ids/acme/pay", 200, `0`))
		defer f.Answer(nil)
		_, err := s.Access(ctx, k)
		require.ErrorContains(t, err, "no project")
	})
}

func TestTheCloneAddressIsTheForgesAndOnlyOnThisHost(t *testing.T) {
	f := newFixture(t)
	s, k := session(t, f, "acme/pay")
	for name, body := range map[string]string{
		"none":         `{"http":"","ssh":""}`,
		"too long":     `{"http":"https://` + f.host + "/" + strings.Repeat("a", 300) + `/x.git"}`,
		"unparseable":  `{"http":"https://token:secret@` + f.host + `/acme/pay.git"}`,
		"another host": `{"http":"https://elsewhere.example/acme/pay.git"}`,
		"not json":     `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			f.Answer(answerOn(http.MethodGet, "/clone-url", 200, body))
			defer f.Answer(nil)
			_, err := s.Repository(t.Context(), k)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret", "a credential the forge put in a clone URL reached an error")
		})
	}
}

func TestRepository(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	t.Run("a project without readable code is no repository", func(t *testing.T) {
		s, k := session(t, f, "acme/issues")
		_, err := s.Repository(ctx, k)
		var nf *forge.NotFoundError
		require.ErrorAs(t, err, &nf)
	})
	t.Run("an empty repository has no default branch yet", func(t *testing.T) {
		s, k := session(t, f, "acme/pay")
		f.mu.Lock()
		f.byPath("acme/pay").empty = true
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.byPath("acme/pay").empty = false; f.mu.Unlock() }()
		repo, err := s.Repository(ctx, k)
		require.NoError(t, err)
		assert.Empty(t, repo.DefaultBranch)
	})
	t.Run("visibility is measured with no credential", func(t *testing.T) {
		s, k := session(t, f, "acme/public")
		repo, err := s.Repository(ctx, k)
		require.NoError(t, err)
		assert.False(t, repo.Private)
		s, k = session(t, f, "acme/pay")
		repo, err = s.Repository(ctx, k)
		require.NoError(t, err)
		assert.True(t, repo.Private)
	})
	t.Run("an anonymous session reads the public and not the private", func(t *testing.T) {
		reg := f.registry(t, forge.Policy{AllowUnboundSchemes: true})
		k := f.key(t, "acme/public")
		s, err := reg.For(k, forge.Anonymous())
		require.NoError(t, err)
		repo, err := s.Repository(ctx, k)
		require.NoError(t, err)
		assert.False(t, repo.Private)
		_, err = s.Repository(ctx, f.key(t, "acme/pay"))
		var nf *forge.NotFoundError
		require.ErrorAs(t, err, &nf, "for a caller with no credential a 401 is not seeing it")
	})
	t.Run("a visibility probe that fails is not an answer", func(t *testing.T) {
		s, k := session(t, f, "acme/pay")
		f.Answer(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(500)
				return true
			}
			return false
		})
		defer f.Answer(nil)
		_, err := s.Repository(ctx, k)
		require.ErrorContains(t, err, "visible without a credential")
	})
	for name, body := range map[string]string{
		"quoted":   `"trunk"`,
		"bad json": `"trunk`,
		"too long": strings.Repeat("b", 300),
	} {
		t.Run("a default branch spelled "+name, func(t *testing.T) {
			s, k := session(t, f, "acme/pay")
			f.Answer(answerOn(http.MethodGet, "/default-branch", 200, body))
			defer f.Answer(nil)
			repo, err := s.Repository(ctx, k)
			if name == "quoted" {
				require.NoError(t, err)
				assert.Equal(t, "trunk", repo.DefaultBranch)
				return
			}
			require.Error(t, err)
		})
	}
	t.Run("an oversized answer is refused, not truncated", func(t *testing.T) {
		s, k := session(t, f, "acme/pay")
		f.Answer(answerOn(http.MethodGet, "/default-branch", 200, strings.Repeat("x", 8<<20+1)))
		defer f.Answer(nil)
		_, err := s.Repository(ctx, k)
		require.ErrorContains(t, err, "more than")
	})
}

func TestListing(t *testing.T) {
	f := newFixture(t)
	s := sessionOn(t, f, "acme/pay")
	ctx := t.Context()

	f.mu.Lock()
	f.listPage = 2
	f.mu.Unlock()
	var got []string
	require.NoError(t, s.ListRepositories(ctx, func(r forge.Repository) bool {
		got = append(got, r.Key.FullName())
		return true
	}))
	assert.Contains(t, got, "acme/team/widget", "paged past the first page")
	assert.NotContains(t, got, "acme/issues", "a project whose code is unreadable is not a repository")
	assert.NotContains(t, got, "acme", "a root project has no key")

	t.Run("an entry with no id is skipped", func(t *testing.T) {
		f.Answer(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/~api/projects" {
				return false
			}
			if r.URL.Query().Get("offset") != "0" {
				fmt.Fprint(w, `[]`)
				return true
			}
			fmt.Fprint(w, `[{"id":0,"path":"acme/x"}]`)
			return true
		})
		defer f.Answer(nil)
		n := 0
		require.NoError(t, s.ListRepositories(ctx, func(forge.Repository) bool { n++; return true }))
		assert.Zero(t, n)
	})
	t.Run("a record that cannot be read fails the listing", func(t *testing.T) {
		f.Answer(answerOn(http.MethodGet, "/clone-url", 500, ""))
		defer f.Answer(nil)
		require.Error(t, s.ListRepositories(ctx, func(forge.Repository) bool { return true }))
	})
	t.Run("a refused credential is said so", func(t *testing.T) {
		f.Answer(answerOn(http.MethodGet, "/~api/projects", 401, ""))
		defer f.Answer(nil)
		var ce *forge.CredentialError
		require.ErrorAs(t, s.ListRepositories(ctx, func(forge.Repository) bool { return true }), &ce)
	})
	t.Run("a forge that never stops paging is stopped", func(t *testing.T) {
		f.Answer(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/~api/projects" {
				return false
			}
			fmt.Fprint(w, `[{"id":1,"path":"acme"}]`)
			return true
		})
		defer f.Answer(nil)
		require.ErrorContains(t, s.ListRepositories(ctx, func(forge.Repository) bool { return true }), "refusing to keep paging")
	})
}

func TestCreate(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	private := forge.CreateOptions{Private: true}

	t.Run("under a nested parent", func(t *testing.T) {
		s, k := session(t, f, "acme/team/gadget")
		repo, err := s.Create(ctx, k, private)
		require.NoError(t, err)
		assert.Equal(t, k, repo.Key)
	})
	t.Run("under a parent that is not there", func(t *testing.T) {
		s, k := session(t, f, "nowhere/gadget")
		_, err := s.Create(ctx, k, private)
		require.ErrorContains(t, err, "no project")
	})
	t.Run("a parent lookup that fails is not a missing parent", func(t *testing.T) {
		s, k := session(t, f, "acme/other")
		f.Answer(answerOn(http.MethodGet, "/ids/acme", 500, ""))
		defer f.Answer(nil)
		_, err := s.Create(ctx, k, private)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "no project")
	})
	t.Run("an id that names nothing", func(t *testing.T) {
		s, k := session(t, f, "acme/other")
		f.Answer(answerOn(http.MethodPost, "/~api/projects", 200, `0`))
		defer f.Answer(nil)
		_, err := s.Create(ctx, k, private)
		require.ErrorContains(t, err, "no id")
	})
	t.Run("a public project asked for and a private one made is taken back", func(t *testing.T) {
		s, k := session(t, f, "acme/open")
		_, err := s.Create(ctx, k, forge.CreateOptions{Private: false})
		require.ErrorContains(t, err, "has been removed")
		f.mu.Lock()
		assert.Nil(t, f.byPath("acme/open"), "the project that came out wrong is still there")
		f.mu.Unlock()
	})
	t.Run("made somewhere else is taken back", func(t *testing.T) {
		s, k := session(t, f, "acme/where")
		f.Answer(answerOn(http.MethodGet, "/clone-url", 200, `{"http":"https://`+f.host+`/acme/elsewhere.git","ssh":""}`))
		_, err := s.Create(ctx, k, private)
		f.Answer(nil)
		require.ErrorContains(t, err, "the forge created")
	})
	t.Run("made without readable code is taken back", func(t *testing.T) {
		s, k := session(t, f, "acme/blind")
		f.Answer(answerOn(http.MethodGet, "/default-branch", 403, ""))
		_, err := s.Create(ctx, k, private)
		f.Answer(nil)
		require.ErrorContains(t, err, "may not read its code")
	})
	t.Run("and when taking it back fails too, both are said", func(t *testing.T) {
		s, k := session(t, f, "acme/stuck")
		f.Answer(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodDelete {
				w.WriteHeader(500)
				return true
			}
			return false
		})
		_, err := s.Create(ctx, k, forge.CreateOptions{Private: false})
		f.Answer(nil)
		require.ErrorContains(t, err, "could not be removed again")
	})
}

func TestDeleteOfAMissingProject(t *testing.T) {
	f := newFixture(t)
	s, k := session(t, f, "acme/nope")
	var nf *forge.NotFoundError
	require.ErrorAs(t, s.Delete(t.Context(), k), &nf)
	s, k = session(t, f, "acme/site")
	f.Answer(answerOn(http.MethodDelete, "", 404, ""))
	require.ErrorAs(t, s.Delete(t.Context(), k), &nf)
}

func TestHooks(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s, k := session(t, f, "acme/site")
	callback, err := url.Parse("https://build.example/hooks/a")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)

	hook, err := s.RegisterPushHook(ctx, k, callback, secret)
	require.NoError(t, err)

	t.Run("every other setting is written back as it was read", func(t *testing.T) {
		f.mu.Lock()
		last := f.saved[len(f.saved)-1]
		f.mu.Unlock()
		assert.JSONEq(t, `[{"enabled":true,"branches":"main"}]`, string(last["branchProtections"]))
	})
	t.Run("a second hook to one callback is refused", func(t *testing.T) {
		_, err := s.RegisterPushHook(ctx, k, callback, secret)
		require.ErrorContains(t, err, "already has a webhook")
	})
	t.Run("a path resolving to another project is not hooked", func(t *testing.T) {
		f.Answer(answerOn(http.MethodGet, "/clone-url", 200, `{"http":"https://`+f.host+`/acme/pay.git"}`))
		defer f.Answer(nil)
		_, err := s.RegisterPushHook(ctx, k, callback, secret)
		require.ErrorContains(t, err, "resolved it to")
	})
	for name, body := range map[string]string{
		"null":              `null`,
		"hooks not a list":  `{"webHooks":{}}`,
		"a hook not a hook": `{"webHooks":[7]}`,
		"no answer":         `<html>`,
	} {
		t.Run("settings answered as "+name, func(t *testing.T) {
			f.Answer(answerOn(http.MethodGet, "/setting", 200, body))
			defer f.Answer(nil)
			_, err := s.RegisterPushHook(ctx, k, callback, secret)
			require.Error(t, err)
			_, err = s.ListPushHooks(ctx, k)
			require.Error(t, err)
			require.Error(t, s.RemovePushHook(ctx, k, hook.Handle))
		})
	}
	t.Run("settings with no webhooks field at all", func(t *testing.T) {
		f.Answer(answerOn(http.MethodGet, "/setting", 200, `{"branchProtections":[]}`))
		defer f.Answer(nil)
		hooks, err := s.ListPushHooks(ctx, k)
		require.NoError(t, err)
		assert.Empty(t, hooks)
	})
	t.Run("a save the forge refuses is an error", func(t *testing.T) {
		f.Answer(answerOn(http.MethodPost, "/setting", 403, ""))
		defer f.Answer(nil)
		other, err := url.Parse("https://build.example/hooks/b")
		require.NoError(t, err)
		_, err = s.RegisterPushHook(ctx, k, other, secret)
		require.Error(t, err)
	})
	t.Run("operations on a project that is not there", func(t *testing.T) {
		s, k := session(t, f, "acme/nope")
		_, err := s.ListPushHooks(ctx, k)
		var nf *forge.NotFoundError
		require.ErrorAs(t, err, &nf)
		require.ErrorAs(t, s.RemovePushHook(ctx, k, "x"), &nf)
		_, err = s.RegisterPushHook(ctx, k, callback, secret)
		require.ErrorAs(t, err, &nf)
	})
	t.Run("a hook without the right to see the settings", func(t *testing.T) {
		s, k := session(t, f, "acme/pay")
		_, err := s.ListPushHooks(ctx, k)
		require.Error(t, err)
		_, err = s.RegisterPushHook(ctx, k, callback, secret)
		require.Error(t, err)
	})
	t.Run("removing leaves the others", func(t *testing.T) {
		other, err := url.Parse("https://build.example/hooks/c")
		require.NoError(t, err)
		second, err := s.RegisterPushHook(ctx, k, other, secret)
		require.NoError(t, err)
		require.NoError(t, s.RemovePushHook(ctx, k, hook.Handle))
		hooks, err := s.ListPushHooks(ctx, k)
		require.NoError(t, err)
		assert.Equal(t, []forge.InstalledPushHook{{Handle: second.Handle, Callback: other.String()}}, hooks)
	})
}

func TestPushReadsOnlyWhatTheHookWasRegisteredFor(t *testing.T) {
	f := newFixture(t)
	reg := f.registry(t, forge.Policy{AllowUnboundSchemes: true})
	k := f.key(t, "acme/site")
	s, err := reg.For(k, forge.Token(goodToken))
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example/hooks/p")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook, err := s.RegisterPushHook(t.Context(), k, callback, secret)
	require.NoError(t, err)

	good, err := f.Delivery(hook, "refs/heads/main", strings.Repeat("a", 40))
	require.NoError(t, err)
	header := good.Header.Get("X-Tau-Repository")

	deliver := func(body string, header string) error {
		hdr, err := forge.Sign(hook.Scheme, hook.Secret, []byte(body), "", testNow)
		require.NoError(t, err)
		if header != "" {
			hdr.Set("X-Tau-Repository", header)
		}
		_, err = reg.VerifyPush(hook, forge.Delivery{Header: hdr, Body: []byte(body)})
		return err
	}
	id, _, ok := strings.Cut(header, " ")
	require.True(t, ok)
	push := `{"type":"RefUpdated","projectId":` + id + `,"refName":"refs/heads/main","newCommitId":"` + strings.Repeat("a", 40) + `"}`

	require.NoError(t, deliver(push, header))
	require.ErrorIs(t, deliver(`{"type":"IssueOpened"}`, header), forge.ErrNotAPush)
	require.ErrorContains(t, deliver(`{`, header), "unreadable")
	require.ErrorContains(t, deliver(push, ""), "no X-Tau-Repository")
	require.ErrorContains(t, deliver(push, strings.Repeat("9", 400)), "implausibly long")
	for _, bad := range []string{"no-space", "x https://a/b/c.git", "0 https://a/b/c.git", "-1 https://a/b/c.git"} {
		require.ErrorContains(t, deliver(push, bad), "is not", bad)
	}
	require.ErrorContains(t, deliver(push, id+" not a url"), "no repository this layer can address")

	descendant := strings.Replace(push, `"projectId":`+id, `"projectId":999`, 1)
	err = deliver(descendant, header)
	require.ErrorContains(t, err, "descendant", "a hook fires for its project's subtree, and a descendant is not this repository")
}
