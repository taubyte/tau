package bitbucket_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucket"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// routes is a server answering "METHOD path" — or "METHOD path?page=N" when
// one is set — with a body, and 404 to anything it was not told about. It
// records what it saw.
type routes struct {
	mu   sync.Mutex
	seen []*http.Request
	m    map[string]string
}

func (rs *routes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rs.mu.Lock()
	rs.seen = append(rs.seen, r.Clone(r.Context()))
	base := r.Method + " " + r.URL.Path
	body, ok := rs.m[base+"?page="+r.URL.Query().Get("page")]
	if !ok {
		body, ok = rs.m[base]
	}
	rs.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, strings.ReplaceAll(body, "HOST", r.Host))
}

func on(t *testing.T, m map[string]string) (*forge.Session, repokey.Key, *routes) {
	t.Helper()
	rs := &routes{m: m}
	sess, key := sessionOn(t, rs, "acme/pay")
	return sess, key, rs
}

const (
	repoPay = `{"full_name":"acme/pay","is_private":true,"mainbranch":{"name":"main"},
	"links":{"clone":[{"name":"https","href":"https://octo@HOST/acme/pay.git"},{"name":"ssh","href":"git@HOST:acme/pay.git"}]}}`

	pathRepo = "GET /2.0/repositories/acme/pay"
	pathPerm = "GET /2.0/user/permissions/repositories"
	pathList = "GET /2.0/repositories"
)

type recordingRT struct {
	mu   sync.Mutex
	sent []*http.Request
	body string
}

func (c *recordingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.sent = append(c.sent, r)
	c.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func hostSession(t *testing.T, host string, rt http.RoundTripper) *forge.Session {
	t.Helper()
	drv, err := bitbucket.New(host, bitbucket.Transport(rt))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	sess, err := reg.Host(host, forge.Token(goodToken))
	require.NoError(t, err)
	return sess
}

func TestBitbucketOrgIsAskedAtItsAPIHostWithABearerToken(t *testing.T) {
	rt := &recordingRT{body: `{"username":"octo","uuid":"{u}"}`}
	sess := hostSession(t, "bitbucket.org", rt)

	id, err := sess.Whoami(ctx())
	require.NoError(t, err)
	assert.Equal(t, forge.Identity{Login: "octo", Subject: "{u}"}, id)
	require.Len(t, rt.sent, 1)
	assert.Equal(t, "https://api.bitbucket.org/2.0/user", rt.sent[0].URL.String())
	assert.Equal(t, "Bearer "+goodToken, rt.sent[0].Header.Get("Authorization"))

	k, err := repokey.Parse("https://bitbucket.org/acme/pay")
	require.NoError(t, err)
	addr, err := sess.CloneAddress(k, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "https://bitbucket.org/acme/pay.git", HTTPUsername: "x-token-auth"}, addr)
	ssh, err := sess.CloneAddress(k, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, forge.Address{URL: "ssh://git@bitbucket.org/acme/pay.git", SSHAccount: "git"}, ssh)
}

func TestAnyOtherHostServesTheSameAPIOnItself(t *testing.T) {
	rt := &recordingRT{body: `{"username":"octo","uuid":"{u}"}`}
	sess := hostSession(t, "bitbucket.example", rt)
	_, err := sess.Whoami(ctx())
	require.NoError(t, err)
	assert.Equal(t, "https://bitbucket.example/2.0/user", rt.sent[0].URL.String())
}

func TestWhoamiRefusesACredentialBelongingToNobody(t *testing.T) {
	for name, body := range map[string]string{
		"no username": `{"uuid":"{u}"}`,
		"no uuid":     `{"username":"octo"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sess, _, _ := on(t, map[string]string{"GET /2.0/user": body})
			_, err := sess.Whoami(ctx())
			require.Error(t, err)
		})
	}
}

func TestAccessMapsBitbucketsPermissionOntoTausRungs(t *testing.T) {
	type testCase struct {
		name   string
		values string
		want   forge.Access
	}
	for _, tc := range []testCase{
		{"admin", `[{"permission":"admin","repository":{"full_name":"acme/pay"}}]`, forge.AccessAdmin},
		{"write", `[{"permission":"write","repository":{"full_name":"acme/pay"}}]`, forge.AccessWrite},
		{"read", `[{"permission":"read","repository":{"full_name":"acme/pay"}}]`, forge.AccessRead},
		{"no grant on a readable repository is read and nothing more", `[]`, forge.AccessRead},
		{"the highest of several grants", `[{"permission":"read","repository":{"full_name":"acme/pay"}},{"permission":"admin","repository":{"full_name":"ACME/Pay"}}]`, forge.AccessAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, key, rs := on(t, map[string]string{pathRepo: repoPay, pathPerm: `{"values":` + tc.values + `}`})
			got, err := sess.Access(ctx(), key)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			last := rs.seen[len(rs.seen)-1]
			assert.Equal(t, `repository.full_name="acme/pay"`, last.URL.Query().Get("q"))
		})
	}
}

func TestAccessRefusesAnAnswerItCannotStandBehind(t *testing.T) {
	type testCase struct{ name, repo, perm string }
	for _, tc := range []testCase{
		{"a permission this driver does not know", repoPay, `{"values":[{"permission":"owner","repository":{"full_name":"acme/pay"}}]}`},
		{"a grant on another repository", repoPay, `{"values":[{"permission":"admin","repository":{"full_name":"acme/other"}}]}`},
		{"a full name disagreeing with the clone link", strings.Replace(repoPay, `"full_name":"acme/pay"`, `"full_name":"acme/other"`, 1), `{"values":[]}`},
		{"a full name that would break out of the filter", strings.Replace(repoPay, `"full_name":"acme/pay"`, `"full_name":"acme/pay\" OR \"x"`, 1), `{"values":[]}`},
		{"a permission lookup that fails", repoPay, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]string{pathRepo: tc.repo}
			if tc.perm != "" {
				m[pathPerm] = tc.perm
			}
			sess, key, _ := on(t, m)
			got, err := sess.Access(ctx(), key)
			require.Error(t, err)
			assert.Equal(t, forge.AccessNone, got)
		})
	}
}

func TestARepositoryIsReadFromItsHTTPSCloneLink(t *testing.T) {
	sess, key, _ := on(t, map[string]string{pathRepo: repoPay})
	repo, err := sess.Repository(ctx(), key)
	require.NoError(t, err)
	assert.Equal(t, key, repo.Key)
	assert.NotContains(t, repo.HTTPS, "octo@", "the username Bitbucket writes into a clone link is not the identity a clone spends")
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.True(t, repo.Private)
	assert.True(t, strings.HasPrefix(repo.SSH, "git@"))

	t.Run("no main branch yet", func(t *testing.T) {
		sess, key, _ := on(t, map[string]string{pathRepo: strings.Replace(repoPay, `"mainbranch":{"name":"main"},`, `"mainbranch":null,`, 1)})
		repo, err := sess.Repository(ctx(), key)
		require.NoError(t, err)
		assert.Empty(t, repo.DefaultBranch)
	})
}

func TestARepositoryWhoseCloneLinkNamesNothingIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no https link":          `{"full_name":"acme/pay","links":{"clone":[{"name":"ssh","href":"git@HOST:acme/pay.git"}]}}`,
		"an implausible link":    `{"links":{"clone":[{"name":"https","href":"https://HOST/acme/` + strings.Repeat("a", 600) + `"}]}}`,
		"a link with a password": `{"links":{"clone":[{"name":"https","href":"https://u:pw-hunter2@HOST/acme/pay.git"}]}}`,
		"a link on another host": `{"links":{"clone":[{"name":"https","href":"https://elsewhere.example/acme/pay.git"}]}}`,
		"an unreadable body":     `{"links":`,
	} {
		t.Run(name, func(t *testing.T) {
			sess, key, _ := on(t, map[string]string{pathRepo: body})
			_, err := sess.Repository(ctx(), key)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "pw-hunter2")
			assert.NotContains(t, err.Error(), strings.Repeat("a", 600))
		})
	}
}

func TestTheListingPagesThroughNextAndSkipsWhatItCannotName(t *testing.T) {
	sess, _, rs := on(t, map[string]string{
		pathList + "?page=1": `{"values":[` + repoPay + `,{"links":{"clone":[]}}],"next":"https://HOST/2.0/repositories?page=2&pagelen=100&role=member"}`,
		pathList + "?page=2": `{"values":[` + strings.ReplaceAll(repoPay, "acme/pay", "acme/site") + `]}`,
	})
	var got []string
	require.NoError(t, sess.ListRepositories(ctx(), func(r forge.Repository) bool {
		got = append(got, r.Key.FullName())
		return true
	}))
	assert.Equal(t, []string{"acme/pay", "acme/site"}, got)
	require.Len(t, rs.seen, 2)
	assert.Equal(t, "2", rs.seen[1].URL.Query().Get("page"))
	assert.Equal(t, "member", rs.seen[1].URL.Query().Get("role"))
}

func TestTheListingStopsWhenTheCallerStops(t *testing.T) {
	sess, _, rs := on(t, map[string]string{
		pathList: `{"values":[` + repoPay + `,` + repoPay + `],"next":"https://HOST/2.0/repositories?page=2&pagelen=100&role=member"}`,
	})
	calls := 0
	require.NoError(t, sess.ListRepositories(ctx(), func(forge.Repository) bool { calls++; return false }))
	assert.Equal(t, 1, calls)
	assert.Len(t, rs.seen, 1)
}

func TestANextLinkThisDriverDidNotAskInTheShapeOfIsRefused(t *testing.T) {
	for name, next := range map[string]string{
		"another host":             "https://elsewhere.example/2.0/repositories?page=2&pagelen=100&role=member",
		"another path":             "https://HOST/2.0/repositories/acme?page=2&pagelen=100&role=member",
		"another value of a param": "https://HOST/2.0/repositories?page=2&pagelen=100&role=owner",
		"a cursor, not a page":     "https://HOST/2.0/repositories?after=xyz&pagelen=100&role=member",
		"a page that skips":        "https://HOST/2.0/repositories?page=3&pagelen=100&role=member",
		"not a URL":                "https://HOST/%zz",
	} {
		t.Run(name, func(t *testing.T) {
			sess, _, rs := on(t, map[string]string{pathList: `{"values":[` + repoPay + `],"next":"` + next + `"}`})
			err := sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })
			require.Error(t, err)
			assert.Len(t, rs.seen, 1, "the listing went on after a next link it could not read")
		})
	}
}

func TestTheListingStopsPagingEventually(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(p+1))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"values":[{}],"next":"https://`+r.Host+r.URL.Path+`?`+q.Encode()+`"}`)
	})
	sess, _ := sessionOn(t, h, "acme/pay")
	err := sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to keep paging")
}

func TestAListingThatFailsIsAnError(t *testing.T) {
	sess, _ := sessionOn(t, answer(http.StatusUnauthorized, `{}`), "acme/pay")
	err := sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })
	var ce *forge.CredentialError
	require.ErrorAs(t, err, &ce)
}

func TestCreatePostsToTheKeyItWillBeAddressedBy(t *testing.T) {
	sess, key, rs := on(t, map[string]string{"POST /2.0/repositories/acme/pay": repoPay})
	repo, err := sess.Create(ctx(), key, forge.CreateOptions{Description: "d", Private: true})
	require.NoError(t, err)
	assert.Equal(t, key, repo.Key)
	require.Len(t, rs.seen, 1, "Create on Bitbucket is one request; there is no personal-account branch to take")

	t.Run("a repository created somewhere else is refused", func(t *testing.T) {
		sess, key, _ := on(t, map[string]string{"POST /2.0/repositories/acme/pay": strings.ReplaceAll(repoPay, "acme/pay", "acme/other")})
		_, err := sess.Create(ctx(), key, forge.CreateOptions{})
		require.Error(t, err)
	})
	t.Run("a created repository with no nameable link is refused", func(t *testing.T) {
		sess, key, _ := on(t, map[string]string{"POST /2.0/repositories/acme/pay": `{"links":{"clone":[]}}`})
		_, err := sess.Create(ctx(), key, forge.CreateOptions{})
		require.Error(t, err)
	})
	t.Run("a workspace that refuses", func(t *testing.T) {
		sess, key := sessionOn(t, answer(http.StatusForbidden, `{}`), "acme/pay")
		_, err := sess.Create(ctx(), key, forge.CreateOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
	})
}

func TestTheDeployKeyBodyHasNoWayToAskForWrite(t *testing.T) {
	var body map[string]any
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":12,"key":"ssh-ed25519 AAAA","label":"tau"}`)
	})
	sess, key := sessionOn(t, h, "acme/pay")
	got, err := sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA")
	require.NoError(t, err)
	assert.Equal(t, forge.Handle("12"), got)
	assert.Equal(t, map[string]any{"key": "ssh-ed25519 AAAA", "label": "tau"}, body)
}

func TestTheHookSubscribesToPushesAndNothingElse(t *testing.T) {
	var body map[string]any
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"uuid":"{abc}"}`)
	})
	sess, key := sessionOn(t, h, "acme/pay")
	cb, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook, err := sess.RegisterPushHook(ctx(), key, cb, secret)
	require.NoError(t, err)
	assert.Equal(t, forge.Handle("{abc}"), hook.Handle)
	assert.Equal(t, forge.SchemeBitbucketSHA256, hook.Scheme)
	wire, err := hook.Scheme.Wire(secret)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"description": "tau", "url": cb.String(), "active": true,
		"events": []any{"repo:push"}, "secret": wire,
	}, body)
}

func TestAResourceWithNoUsableHandleIsRefused(t *testing.T) {
	cb, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	register := func(s *forge.Session, k repokey.Key) error {
		_, err := s.RegisterPushHook(ctx(), k, cb, secret)
		return err
	}

	type testCase struct {
		route, body string
		call        func(*forge.Session, repokey.Key) error
	}
	for name, tc := range map[string]testCase{
		"a deploy key with id zero": {"POST /2.0/repositories/acme/pay/deploy-keys", `{"id":0}`, func(s *forge.Session, k repokey.Key) error {
			_, err := s.InstallDeployKey(ctx(), k, "t", "ssh-ed25519 AAAA")
			return err
		}},
		"a hook with no uuid":              {"POST /2.0/repositories/acme/pay/hooks", `{}`, register},
		"a hook whose uuid spans segments": {"POST /2.0/repositories/acme/pay/hooks", `{"uuid":"a/b"}`, register},
		"a listed key with id zero": {"GET /2.0/repositories/acme/pay/deploy-keys", `{"values":[{"id":0}]}`, func(s *forge.Session, k repokey.Key) error {
			_, err := s.ListDeployKeys(ctx(), k)
			return err
		}},
		"a listed hook whose uuid is a dot segment": {"GET /2.0/repositories/acme/pay/hooks", `{"values":[{"uuid":".."}]}`, func(s *forge.Session, k repokey.Key) error {
			_, err := s.ListPushHooks(ctx(), k)
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			sess, key, _ := on(t, map[string]string{tc.route: tc.body})
			require.Error(t, tc.call(sess, key))
		})
	}
}

func TestListingsOfKeysAndHooksPageToTheEnd(t *testing.T) {
	keys := "GET /2.0/repositories/acme/pay/deploy-keys"
	sess, key, _ := on(t, map[string]string{
		keys + "?page=1":                       `{"values":[{"id":1,"key":"k1","label":"a"}],"next":"https://HOST/2.0/repositories/acme/pay/deploy-keys?page=2&pagelen=100"}`,
		keys + "?page=2":                       `{"values":[{"id":2,"key":"k2","label":"b"}]}`,
		"GET /2.0/repositories/acme/pay/hooks": `{"values":[{"uuid":"{1}","url":"https://x"}]}`,
	})
	got, err := sess.ListDeployKeys(ctx(), key)
	require.NoError(t, err)
	assert.Equal(t, []forge.InstalledDeployKey{{Handle: "1", Title: "a", PublicKey: "k1"}, {Handle: "2", Title: "b", PublicKey: "k2"}}, got)
	hooks, err := sess.ListPushHooks(ctx(), key)
	require.NoError(t, err)
	assert.Equal(t, []forge.InstalledPushHook{{Handle: "{1}", Callback: "https://x"}}, hooks)

	t.Run("a listing that fails", func(t *testing.T) {
		sess, key := sessionOn(t, answer(http.StatusInternalServerError, `{}`), "acme/pay")
		_, err := sess.ListDeployKeys(ctx(), key)
		require.Error(t, err)
		_, err = sess.ListPushHooks(ctx(), key)
		require.Error(t, err)
	})
}

func TestRemovingByAHandleThatNamesSomethingElseIsRefusedBeforeTheWire(t *testing.T) {
	for _, h := range []forge.Handle{"", "a/b", "..", "."} {
		t.Run(fmt.Sprintf("%q", h), func(t *testing.T) {
			sess, key, rs := on(t, map[string]string{})
			require.Error(t, sess.RemoveDeployKey(ctx(), key, h))
			require.Error(t, sess.RemovePushHook(ctx(), key, h))
			assert.Empty(t, rs.seen)
		})
	}
}

func TestA404OnAResourceIsNotAVerdictAboutTheRepository(t *testing.T) {
	sess, key, _ := on(t, map[string]string{})
	for _, err := range []error{
		sess.RemoveDeployKey(ctx(), key, "7"),
		sess.RemovePushHook(ctx(), key, "{7}"),
	} {
		require.Error(t, err)
		var nf *forge.NotFoundError
		assert.False(t, errors.As(err, &nf))
	}
}

func TestAnAnswerThisDriverWillNotFinishReadingIsAnError(t *testing.T) {
	big := `{"username":"` + strings.Repeat("a", 9<<20) + `"}`
	sess, _ := sessionOn(t, answer(http.StatusOK, big), "acme/pay")
	_, err := sess.Whoami(ctx())
	require.Error(t, err)
}
