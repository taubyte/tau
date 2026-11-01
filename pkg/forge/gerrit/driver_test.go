package gerrit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gerrit"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// odd is a Gerrit answering whatever a test says, for the answers the fixture
// is too well-behaved to give.
type odd struct {
	host string
	reg  *forge.Registry
}

func serve(t *testing.T, h http.HandlerFunc, opts ...gerrit.Option) *odd {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	drv, err := gerrit.New(host, append([]gerrit.Option{gerrit.Transport(srv.Client().Transport)}, opts...)...)
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	return &odd{host: host, reg: reg}
}

func (o *odd) key(t *testing.T, full string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse("https://" + o.host + "/" + full)
	require.NoError(t, err)
	return k
}

func (o *odd) session(t *testing.T, cred forge.Credential) *forge.Session {
	t.Helper()
	s, err := o.reg.Host(o.host, cred)
	require.NoError(t, err)
	return s
}

func json200(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, ")]}'\n"+body)
}

func TestNewRefusesWhatIsNotAHostOrAnOption(t *testing.T) {
	_, err := gerrit.New("")
	assert.Error(t, err)
	_, err = gerrit.New("Gerrit.Example")
	assert.Error(t, err, "a host repokey would spell differently")
	_, err = gerrit.New("gerrit.example", gerrit.Transport(nil))
	assert.Error(t, err)
	_, err = gerrit.New("gerrit.example", gerrit.Membership("no-colon"))
	assert.ErrorContains(t, err, "username:http-password")
	_, err = gerrit.New("gerrit.example", gerrit.Membership(":pw"))
	assert.Error(t, err)

	d, err := gerrit.New("127.0.0.1:8080", gerrit.Plaintext())
	require.NoError(t, err)
	c, err := d.Bind(forge.Token("u:p"))
	require.NoError(t, err)
	addr, err := c.(forge.CloneAddresses).CloneAddress(repokey.Key{Host: "127.0.0.1:8080", Owner: "acme", Name: "pay"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080/acme/pay", addr.URL)
}

func TestBindRefusesACredentialGerritCannotSpend(t *testing.T) {
	d, err := gerrit.New("gerrit.example")
	require.NoError(t, err)
	_, err = d.Bind(forge.Credential{})
	assert.ErrorIs(t, err, forge.ErrNoCredential)
	_, err = d.Bind(forge.Token("just-a-token"))
	assert.ErrorContains(t, err, "username:http-password")
	_, err = d.Bind(forge.Token("user:"))
	assert.Error(t, err)
}

func TestAddressableAcceptsEveryDepthOnItsOwnHost(t *testing.T) {
	d, err := gerrit.New("gerrit.example")
	require.NoError(t, err)
	assert.NoError(t, d.Addressable(repokey.Key{Host: "gerrit.example", Name: "root-project"}))
	assert.NoError(t, d.Addressable(repokey.Key{Host: "gerrit.example", Owner: "a/b/c", Name: "d"}))
	assert.Error(t, d.Addressable(repokey.Key{Host: "other.example", Owner: "a", Name: "b"}))
	assert.Error(t, d.Addressable(repokey.Key{Host: "gerrit.example", Owner: "a"}))
}

func TestAnAnonymousSessionReadsWithoutTheAuthenticatedPrefix(t *testing.T) {
	var seen []string
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "" {
			t.Errorf("an anonymous request carried Authorization")
		}
		switch r.URL.EscapedPath() {
		case "/projects/acme%2Fpay":
			json200(w, `{"name":"acme/pay"}`)
		case "/projects/acme%2Fpay/HEAD":
			json200(w, `"refs/heads/main"`)
		default:
			http.NotFound(w, r)
		}
	})
	s := o.session(t, forge.Anonymous())
	repo, err := s.Repository(t.Context(), o.key(t, "acme/pay"))
	require.NoError(t, err)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.Empty(t, repo.SSH, "Gerrit's sshd authenticates accounts, and nobody is not one")
	assert.Equal(t, []string{"/projects/acme%2Fpay", "/projects/acme%2Fpay/HEAD"}, seen)

	_, err = s.CloneAddress(o.key(t, "acme/pay"), forge.SSH)
	assert.Error(t, err)
	addr, err := s.CloneAddress(o.key(t, "acme/pay"), forge.HTTPS)
	require.NoError(t, err)
	assert.Empty(t, addr.HTTPUsername)
	_, err = s.Create(t.Context(), o.key(t, "acme/new"), forge.CreateOptions{})
	assert.ErrorContains(t, err, "anonymous")
}

func TestAProjectNameWithNoKeyIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"empty":       `{"name":""}`,
		"uppercase":   `{"name":"Acme/Pay"}`,
		"dot segment": `{"name":"acme/../pay"}`,
		"too long":    `{"name":"` + strings.Repeat("a", 300) + `"}`,
		"unparseable": `{"name":"acme/pay?evil"}`,
	} {
		t.Run(name, func(t *testing.T) {
			o := serve(t, func(w http.ResponseWriter, r *http.Request) { json200(w, body) })
			_, err := o.session(t, forge.Token("u:p")).Repository(t.Context(), o.key(t, "acme/pay"))
			assert.Error(t, err)
		})
	}
}

func TestAHeadThatIsNotABranchIsRefused(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/HEAD"):
			json200(w, `"0123456789abcdef0123456789abcdef01234567"`)
		case strings.HasSuffix(r.URL.EscapedPath(), "/access"):
			json200(w, `{}`)
		default:
			json200(w, `{"name":"acme/pay"}`)
		}
	})
	_, err := o.session(t, forge.Token("u:p")).Repository(t.Context(), o.key(t, "acme/pay"))
	assert.ErrorContains(t, err, "not a branch")
}

func TestAFailureFetchingHalfARecordIsAnError(t *testing.T) {
	for _, broken := range []string{"/HEAD", "/access"} {
		t.Run(broken, func(t *testing.T) {
			o := serve(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.EscapedPath(), broken):
					w.WriteHeader(http.StatusInternalServerError)
				case strings.HasSuffix(r.URL.EscapedPath(), "/HEAD"):
					json200(w, `"refs/heads/main"`)
				case strings.HasSuffix(r.URL.EscapedPath(), "/access"):
					json200(w, `{"is_owner":true}`)
				default:
					json200(w, `{"name":"acme/pay"}`)
				}
			})
			lvl, err := o.session(t, forge.Token("u:p")).Access(t.Context(), o.key(t, "acme/pay"))
			assert.Error(t, err)
			assert.Equal(t, forge.AccessNone, lvl)
		})
	}
}

func TestABodyThisDriverCannotReadIsAnError(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) { json200(w, `{not json`) })
	_, err := o.session(t, forge.Token("u:p")).Whoami(t.Context())
	assert.ErrorContains(t, err, "cannot read")
}

func TestAnAccountWithNoUsernameIsNobodyThisLayerCanName(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) { json200(w, `{"_account_id":7}`) })
	_, err := o.session(t, forge.Token("u:p")).Whoami(t.Context())
	assert.ErrorContains(t, err, "no username")
}

func TestTheListingPagesAndSkipsOnlyWhatHasNoKey(t *testing.T) {
	const total = 120
	described := 0
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		switch {
		case p == "/a/projects/":
			var start int
			fmt.Sscan(r.URL.Query().Get("S"), &start)
			var parts []string
			for i := start; i < start+50 && i < total; i++ {
				more := ""
				if i == start+49 && i < total-1 {
					more = `,"_more_projects":true`
				}
				parts = append(parts, fmt.Sprintf(`"acme/p%03d":{"id":"x"%s}`, i, more))
			}
			if start == 0 {
				parts = append(parts, `"All-Projects":{"id":"All-Projects"}`)
			}
			json200(w, "{"+strings.Join(parts, ",")+"}")
		case strings.HasSuffix(p, "/HEAD"):
			described++
			json200(w, `"refs/heads/main"`)
		case strings.HasSuffix(p, "/access"):
			json200(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	s := o.session(t, forge.Token("u:p"))
	var got []repokey.Key
	require.NoError(t, s.ListRepositories(t.Context(), func(r forge.Repository) bool {
		got = append(got, r.Key)
		return true
	}))
	assert.Len(t, got, total, "every lowercase project and nothing else")
	assert.Equal(t, total, described)
	assert.Equal(t, o.key(t, "acme/p119"), got[total-1])
}

func TestTheListingFailsRatherThanDropAProject(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/a/projects/" {
			json200(w, `{"acme/pay":{"id":"x"}}`)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	})
	err := o.session(t, forge.Token("u:p")).ListRepositories(t.Context(), func(forge.Repository) bool { return true })
	assert.Error(t, err)
}

func TestAListingThatNeverEndsIsRefused(t *testing.T) {
	pages := 0
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/a/projects/" {
			pages++
			json200(w, `{"Upper/Case":{"_more_projects":true}}`)
			return
		}
		http.NotFound(w, r)
	})
	err := o.session(t, forge.Token("u:p")).ListRepositories(t.Context(), func(forge.Repository) bool { return true })
	assert.ErrorContains(t, err, "refusing to keep paging")
	assert.Equal(t, 1000, pages)
}

func TestAPrivateCreateThatCouldNotBeRestrictedIsAnError(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		case http.MethodPost:
			w.WriteHeader(http.StatusForbidden)
		}
	})
	_, err := o.session(t, forge.Token("u:p")).Create(t.Context(), o.key(t, "acme/new"), forge.CreateOptions{Private: true})
	assert.ErrorContains(t, err, "as readable as its parent allows")
}

func TestACreateTheForgeAnswersAboutAnotherProjectIsRefused(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.EscapedPath(), "/HEAD"):
			json200(w, `"refs/heads/main"`)
		case strings.HasSuffix(r.URL.EscapedPath(), "/access"):
			json200(w, `{"is_owner":true}`)
		default:
			json200(w, `{"name":"acme/elsewhere"}`)
		}
	})
	s := o.session(t, forge.Token("u:p"))
	_, err := s.Create(t.Context(), o.key(t, "acme/new"), forge.CreateOptions{})
	assert.ErrorContains(t, err, "created")
	_, err = s.Create(t.Context(), o.key(t, "acme/new"), forge.CreateOptions{})
	assert.Error(t, err)
}

func TestACreateWhoseRecordCannotBeReadIsAnError(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := o.session(t, forge.Token("u:p")).Create(t.Context(), o.key(t, "acme/new"), forge.CreateOptions{})
	assert.Error(t, err)
}

func TestMembershipRefusesAnAnswerAboutSomebodyElse(t *testing.T) {
	cases := map[string]struct {
		group, member func(w http.ResponseWriter)
		user          string
		want          string
	}{
		"another group": {
			group: func(w http.ResponseWriter) { json200(w, `{"name":"other"}`) },
			user:  "alice", want: "another group",
		},
		"another account": {
			group:  func(w http.ResponseWriter) { json200(w, `{"name":"acme"}`) },
			member: func(w http.ResponseWriter) { json200(w, `{"username":"admin"}`) },
			user:   "alice", want: "another account",
		},
		"an outage on the member": {
			group:  func(w http.ResponseWriter) { json200(w, `{"name":"acme"}`) },
			member: func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) },
			user:   "alice", want: "could not be established",
		},
		"an invisible group": {
			group: func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) },
			user:  "alice", want: "could not be established",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			o := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.EscapedPath(), "/members/") {
					tc.member(w)
					return
				}
				tc.group(w)
			}, gerrit.Membership("warden:pw"))
			ok, err := o.reg.IsActiveMember(t.Context(), o.host, "acme", tc.user)
			assert.ErrorContains(t, err, tc.want)
			assert.False(t, ok)
		})
	}
}

func TestMembershipRefusesAnAccountIdForAUsername(t *testing.T) {
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a refused question reached the wire: %s", r.URL)
	}, gerrit.Membership("warden:pw"))
	for _, u := range []string{"self", "1000000", ""} {
		_, err := o.reg.IsActiveMember(t.Context(), o.host, "acme", u)
		assert.Error(t, err, "%q", u)
	}
	_, err := o.reg.IsActiveMember(t.Context(), o.host, "", "alice")
	assert.Error(t, err)
}
