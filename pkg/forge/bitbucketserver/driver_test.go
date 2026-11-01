package bitbucketserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucketserver"
	"github.com/taubyte/tau/pkg/git/repokey"
)

var ctx = context.Background()

func parse(t *testing.T, s string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse(s)
	require.NoError(t, err)
	return k
}

func TestNewRefusesWhatIsNotADeployment(t *testing.T) {
	for _, host := range []string{"", "Bitbucket.Example.com", "https://bb.example.com"} {
		_, err := bitbucketserver.New(host)
		assert.Error(t, err, host)
	}
	for _, opt := range []bitbucketserver.Option{bitbucketserver.Transport(nil), bitbucketserver.SSHPort(0), bitbucketserver.SSHPort(70000)} {
		_, err := bitbucketserver.New("bb.example.com", opt)
		assert.Error(t, err)
	}
}

func TestBothAddressesNameOneRepository(t *testing.T) {
	drv, err := bitbucketserver.New("bb.example.com")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "bb.example.com", Driver: drv})
	require.NoError(t, err)
	want := parse(t, "https://bb.example.com/proj/repo")

	c, err := drv.Bind(forge.Token("t"))
	require.NoError(t, err)
	https, err := c.(forge.CloneAddresses).CloneAddress(want, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "https://bb.example.com/scm/PROJ/repo.git", HTTPUsername: "x-token-auth"}, https)
	ssh, err := c.(forge.CloneAddresses).CloneAddress(want, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "ssh://git@bb.example.com:7999/PROJ/repo.git", SSHAccount: "git"}, ssh)

	for _, spelling := range []string{https.URL, ssh.URL, "https://alice@bb.example.com/scm/proj/repo.git", "git@bb.example.com:7999/x", "https://bb.example.com/proj/repo"} {
		got, err := reg.Key(spelling)
		require.NoError(t, err)
		if spelling == "git@bb.example.com:7999/x" {
			// scp has no port: that is the repository "7999/x" on ssh's own
			// port, which this forge does not serve, and the parse stands.
			assert.Equal(t, parse(t, spelling), got)
			continue
		}
		assert.Equal(t, want, got, spelling)
	}
	a, b := parse(t, https.URL), parse(t, ssh.URL)
	assert.NotEqual(t, a, b, "repokey has no vocabulary; the forge's spelling is the registry's to apply")

	// Shapes on either host that are not Bitbucket's spelling are not rewritten.
	for _, spelling := range []string{"https://bb.example.com/scm/a/b/c", "ssh://git@bb.example.com:7999/a/b/c", "ssh://git@bb.example.com:7999/c"} {
		got, err := reg.Key(spelling)
		require.NoError(t, err)
		assert.Equal(t, parse(t, spelling), got)
	}

	// A personal repository keeps its "~".
	k, err := reg.Key("https://bb.example.com/scm/~alice/notes.git")
	require.NoError(t, err)
	got, err := c.(forge.CloneAddresses).CloneAddress(k, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://bb.example.com/scm/~alice/notes.git", got.URL)
}

func TestSSHOnItsOwnPortOrHost(t *testing.T) {
	drv, err := bitbucketserver.New("bb.example.com", bitbucketserver.SSHPort(22))
	require.NoError(t, err)
	assert.Empty(t, drv.(forge.Spellings).SpellingHosts(), "ssh on 22 is the https host's own spelling")

	drv, err = bitbucketserver.New("bb.example.com:8443", bitbucketserver.SSHPort(2222))
	require.NoError(t, err)
	assert.Equal(t, []string{"bb.example.com:2222"}, drv.(forge.Spellings).SpellingHosts())

	drv, err = bitbucketserver.New("[::1]:8443")
	require.NoError(t, err)
	assert.Equal(t, []string{"[::1]:7999"}, drv.(forge.Spellings).SpellingHosts())
}

func TestAddressableRefusesWhatBitbucketCannotAddress(t *testing.T) {
	drv, err := bitbucketserver.New("bb.example.com")
	require.NoError(t, err)
	for _, s := range []string{"https://other.example.com/a/b", "https://bb.example.com/b", "https://bb.example.com/a/b/c"} {
		assert.Error(t, drv.Addressable(parse(t, s)), s)
	}
	c, err := drv.Bind(forge.Token("t"))
	require.NoError(t, err)
	bad := parse(t, "https://bb.example.com/a/b/c")
	_, err = c.Repository(ctx, bad)
	assert.Error(t, err)
	_, err = c.(forge.Creator).Create(ctx, bad, forge.CreateOptions{})
	assert.Error(t, err)
	assert.Error(t, c.(forge.Deleter).Delete(ctx, bad))
	_, err = c.(forge.DeployKeys).InstallDeployKey(ctx, bad, "t", "ssh-ed25519 AAAA")
	assert.Error(t, err)
	_, err = c.(forge.DeployKeys).ListDeployKeys(ctx, bad)
	assert.Error(t, err)
	assert.Error(t, c.(forge.DeployKeys).RemoveDeployKey(ctx, bad, "1"))
	_, err = c.(forge.PushHooks).RegisterPushHook(ctx, bad, nil, "w")
	assert.Error(t, err)
	_, err = c.(forge.PushHooks).ListPushHooks(ctx, bad)
	assert.Error(t, err)
	assert.Error(t, c.(forge.PushHooks).RemovePushHook(ctx, bad, "1"))
	_, err = c.(forge.CloneAddresses).CloneAddress(bad, forge.HTTPS)
	assert.Error(t, err)

	dot := parse(t, "https://bb.example.com/a/b")
	dot.Name = ".."
	_, err = c.(forge.CloneAddresses).CloneAddress(dot, forge.HTTPS)
	assert.Error(t, err)
	_, err = drv.Bind(forge.Credential{})
	assert.ErrorIs(t, err, forge.ErrNoCredential)
}

func TestAHandleIsANumber(t *testing.T) {
	f := newFixture(t)
	c, err := newDriver(t, f).Bind(forge.Token(goodToken))
	require.NoError(t, err)
	site := f.key(t, "acme/site")
	for _, h := range []forge.Handle{"", "..", "1/2", "x"} {
		assert.Error(t, c.(forge.DeployKeys).RemoveDeployKey(ctx, site, h))
		assert.Error(t, c.(forge.PushHooks).RemovePushHook(ctx, site, h))
	}
}

func TestADefaultBranchIsAbsentFromAnEmptyRepository(t *testing.T) {
	f := newFixture(t)
	f.emptyDefault = true
	c, err := newDriver(t, f).Bind(forge.Token(goodToken))
	require.NoError(t, err)
	repo, err := c.Repository(ctx, f.key(t, "acme/pay"))
	require.NoError(t, err)
	assert.Empty(t, repo.DefaultBranch)
	assert.True(t, repo.Private)
}

func TestAccessAboutAMovedRepositoryIsRefused(t *testing.T) {
	f := newFixture(t)
	c, err := newDriver(t, f).Bind(forge.Token(goodToken))
	require.NoError(t, err)
	_, err = c.Access(ctx, f.key(t, "acme/billing"))
	assert.ErrorContains(t, err, "answered about")
}

func TestAWrongPermissionDeployKeyIsRemoved(t *testing.T) {
	var removed bool
	srv := canned(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			fmt.Fprint(w, `{"key":{"id":7,"text":"ssh-ed25519 AAAA","label":"t"},"permission":"REPO_WRITE"}`)
		case http.MethodDelete:
			removed = r.URL.Path == "/rest/keys/1.0/projects/ACME/repos/site/ssh/7"
			w.WriteHeader(http.StatusNoContent)
		}
	})
	_, err := srv.client(t).(forge.DeployKeys).InstallDeployKey(ctx, srv.key(t, "acme/site"), "t", "ssh-ed25519 AAAA")
	assert.ErrorContains(t, err, "REPO_WRITE")
	assert.True(t, removed)
}

// cannedServer is one Bitbucket answering whatever a test says, for the
// answers a well-behaved fixture never gives.
type cannedServer struct {
	srv  *httptest.Server
	host string
	drv  forge.Driver
}

func canned(t *testing.T, h http.HandlerFunc) *cannedServer {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	drv, err := bitbucketserver.New(host, bitbucketserver.Transport(srv.Client().Transport))
	require.NoError(t, err)
	return &cannedServer{srv: srv, host: host, drv: drv}
}

func (s *cannedServer) client(t *testing.T) forge.Client {
	t.Helper()
	c, err := s.drv.Bind(forge.Token("t"))
	require.NoError(t, err)
	return c
}

func (s *cannedServer) key(t *testing.T, full string) repokey.Key {
	return parse(t, "https://"+s.host+"/"+full)
}

func (s *cannedServer) repo(project, slug, href string) string {
	b, err := json.Marshal(map[string]any{
		"slug": slug, "name": slug, "project": map[string]string{"key": project},
		"links": map[string]any{"clone": []map[string]string{{"name": "http", "href": href}}},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestAMalformedAnswerIsRefused(t *testing.T) {
	var answer string
	var status int
	var s *cannedServer
	s = canned(t, func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		fmt.Fprint(w, answer)
	})
	c := s.client(t)
	k := s.key(t, "acme/pay")
	scm := "https://" + s.host + "/scm/ACME/pay.git"

	for name, body := range map[string]string{
		"no clone link":       `{"slug":"pay","project":{"key":"ACME"}}`,
		"a long link":         s.repo("ACME", "pay", "https://"+s.host+"/scm/ACME/"+strings.Repeat("a", 300)),
		"an unparsable link":  s.repo("ACME", "pay", "nope"),
		"another host's link": s.repo("ACME", "pay", "https://elsewhere.example.com/scm/ACME/pay.git"),
		"a link elsewhere":    s.repo("ACME", "other", scm),
		"no project":          s.repo("", "pay", scm),
		"not json":            `{`,
	} {
		answer = body
		_, err := c.Repository(ctx, k)
		assert.Error(t, err, name)
	}

	// The default branch is asked separately, and its failure is a failure.
	status = 0
	answer = s.repo("ACME", "pay", scm)
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/branches/default") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/repos") && r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"values":[%s],"isLastPage":true}`, answer)
			return
		}
		fmt.Fprint(w, answer)
	})
	_, err := c.Repository(ctx, k)
	assert.Error(t, err)
	_, err = c.(forge.Creator).Create(ctx, k, forge.CreateOptions{})
	assert.Error(t, err)
	assert.Error(t, c.(forge.Lister).ListRepositories(ctx, func(forge.Repository) bool { return true }),
		"a listing whose default branch cannot be asked is a failure")

	// A creation that lands elsewhere is refused.
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, s.repo("ACME", "other", "https://"+s.host+"/scm/ACME/other.git"))
	})
	_, err = c.(forge.Creator).Create(ctx, k, forge.CreateOptions{})
	assert.ErrorContains(t, err, "created")

	// Zero ids are resources nothing could remove.
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"values":[{"id":0,"key":{"id":0}}],"isLastPage":true}`)
			return
		}
		fmt.Fprint(w, `{"id":0,"key":{"id":0},"permission":"REPO_READ"}`)
	})
	_, err = c.(forge.DeployKeys).InstallDeployKey(ctx, k, "t", "ssh-ed25519 AAAA")
	assert.Error(t, err)
	_, err = c.(forge.DeployKeys).ListDeployKeys(ctx, k)
	assert.Error(t, err)
	_, err = c.(forge.PushHooks).RegisterPushHook(ctx, k, &url.URL{Scheme: "https", Host: "cb.example.com"}, "w")
	assert.Error(t, err)
	_, err = c.(forge.PushHooks).ListPushHooks(ctx, k)
	assert.Error(t, err)

	// A pager that does not move forward is refused, and one that never ends is
	// stopped.
	for _, page := range []string{`{"values":[{"id":1}],"isLastPage":false}`, `{"values":[{"id":1}],"isLastPage":false,"nextPageStart":0}`} {
		s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, page) })
		_, err = c.(forge.PushHooks).ListPushHooks(ctx, k)
		assert.ErrorContains(t, err, "next page")
	}
	n := 0
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprintf(w, `{"values":[{"id":1,"url":"x"}],"isLastPage":false,"nextPageStart":%d}`, n)
	})
	_, err = c.(forge.PushHooks).ListPushHooks(ctx, k)
	assert.ErrorContains(t, err, "refusing to keep paging")

	// A malformed listing entry is skipped, and a short-stopped one is not an
	// error.
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/branches/default") {
			fmt.Fprint(w, `{"displayId":"main"}`)
			return
		}
		fmt.Fprintf(w, `{"values":[{"slug":"x"},%s,%s],"isLastPage":true}`, s.repo("ACME", "pay", scm), s.repo("ACME", "pay", scm))
	})
	var seen int
	require.NoError(t, c.(forge.Lister).ListRepositories(ctx, func(forge.Repository) bool { seen++; return false }))
	assert.Equal(t, 1, seen)
}

func TestWhoamiRefusesNobody(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"no slug": func(w http.ResponseWriter, r *http.Request) {},
		"another user": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "whoami") {
				fmt.Fprint(w, "alice")
				return
			}
			fmt.Fprint(w, `{"slug":"bob","id":1}`)
		},
		"no user": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "whoami") {
				fmt.Fprint(w, "alice")
				return
			}
			w.WriteHeader(http.StatusNotFound)
		},
		"a refusal it cannot read": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "<html>")
		},
	} {
		_, err := canned(t, h).client(t).Whoami(ctx)
		assert.Error(t, err, name)
	}
}

func TestAPermissionRefusalIsNotACredentialRefusal(t *testing.T) {
	s := canned(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"exceptionName":"AuthorisationException"}]}`)
	})
	err := s.client(t).(forge.Deleter).Delete(ctx, s.key(t, "acme/pay"))
	require.Error(t, err)
	var ce *forge.CredentialError
	assert.False(t, errors.As(err, &ce))
	assert.Contains(t, err.Error(), "holds no permission")
}

func TestAPushIsReadOnlyAsOneRef(t *testing.T) {
	f := newFixture(t)
	reg := registry(t, f)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook := forge.Hook{Key: f.key(t, "acme/site"), Handle: "1", Scheme: forge.SchemeBitbucketSHA256, Secret: secret}

	deliver := func(event string, body any) error {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		hdr, err := forge.Sign(hook.Scheme, secret, raw, "d", time.Now())
		require.NoError(t, err)
		hdr.Set("X-Event-Key", event)
		_, err = reg.VerifyPush(hook, forge.Delivery{Header: hdr, Body: raw})
		return err
	}
	repo := map[string]any{"slug": "site", "project": map[string]string{"key": "ACME"}}
	change := func(ref string) map[string]string {
		return map[string]string{"refId": ref, "toHash": strings.Repeat("c", 40), "type": "UPDATE"}
	}

	require.NoError(t, deliver("repo:refs_changed", map[string]any{"repository": repo, "changes": []any{change("refs/tags/v1")}}))
	assert.ErrorIs(t, deliver("pr:opened", map[string]any{}), forge.ErrNotAPush)
	for name, body := range map[string]any{
		"no repository": map[string]any{"changes": []any{change("refs/heads/main")}},
		"no change":     map[string]any{"repository": repo},
		"two changes":   map[string]any{"repository": repo, "changes": []any{change("refs/heads/a"), change("refs/heads/b")}},
		"a note":        map[string]any{"repository": repo, "changes": []any{change("refs/notes/x")}},
		"not an object": []int{1},
	} {
		assert.Error(t, deliver("repo:refs_changed", body), name)
	}
}
