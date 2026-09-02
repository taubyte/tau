package github_test

// SAME-HOST REDIRECTS, FOR EVERY CALL THIS DRIVER MAKES. Everything here is
// httptest and in-process: a forge that answers one repository by pointing at
// ANOTHER REPOSITORY ON ITSELF.
//
// noRedirect is one rule on the client, so it holds for every request or for
// none — but until this file only THREE call shapes said so: Access twice and
// RegisterPushHook once. Measured: a per-method policy that returns nil from
// noRedirect when the first hop's path ends in "/keys" left the whole tree at
// exit 0, while InstallDeployKey returned handle "991" and a nil error, having
// installed a machine credential on attacker/pay. RemoveDeployKey, RemovePushHook,
// Delete, Create, Whoami, ListRepositories and the membership walk were in the
// same position.
//
// THE FORGE HERE ANSWERS THE SECOND HOP ABOUT THE REPOSITORY THAT WAS ASKED FOR,
// which is what makes each row a statement about the redirect rule and not about
// something else. This driver checks the key it derives from a clone URL against
// the key it asked about — Access and Create both do — so a successor that named
// ITSELF would make those rows pass on that check instead, whether or not the hop
// was followed. Naming the victim closes that: if the hop is followed, every call
// below succeeds and returns what a consumer would have believed.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/github"
)

// mover is a forge whose answer to the call under test is a 3xx to another
// namespace ON ITSELF, and which then serves that location as a perfectly
// working API.
//
// Both hops are the same host, so forge.PinnedTransport permits the second one
// and MUST: a per-hop host check is the whole of what a pin can decide. What is
// on the other end of that hop is another repository entirely.
type mover struct {
	status int

	// answered are the paths this forge serves straight, because the call under
	// test needs them BEFORE it makes the request being moved. Without them
	// Create is stopped at its Whoami and the membership walk at its
	// installation lookup, and the write each one exists to make is never the
	// request a redirect is answered to — which is how a per-method policy
	// walks between the rows.
	answered map[string]bool

	mu      sync.Mutex
	arrived []string
}

func newMover(t *testing.T, status int, answered ...string) (*mover, *forge.Registry, *hops, *forge.Session, repokey.Key) {
	t.Helper()
	m := &mover{status: status, answered: map[string]bool{}}
	for _, p := range answered {
		m.answered[p] = true
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")

	// Under the pin, so a request replayed at a second address is a second
	// entry whatever address that was — the forge below only sees the hops that
	// arrived at IT.
	seen := &hops{base: srv.Client().Transport}

	drv, err := github.New(host, github.Transport(seen), github.App(appClientID, testKeyPEM()))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://" + host + "/" + namespace + "/pay")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(goodToken))
	require.NoError(t, err)
	return m, reg, seen, sess, key
}

func (m *mover) serve(w http.ResponseWriter, r *http.Request) {
	// WITH the credential or without it is recorded, because that is what a
	// second hop costs: PinnedTransport attaches the header on every hop whose
	// host matches, and this location's does.
	arrival := r.Method + " " + r.URL.RequestURI()
	if r.Header.Get("Authorization") != "" {
		arrival += " (carrying a credential)"
	}
	m.mu.Lock()
	m.arrived = append(m.arrived, arrival)
	m.mu.Unlock()

	if to, moved := m.elsewhere(r.URL.RequestURI()); moved && !m.answered[r.URL.Path] {
		// A renamed repository, a transferred one, a forge behind a rewrite:
		// the answer names a location on the very host the credential is for.
		http.Redirect(w, r, to, m.status)
		return
	}
	m.answer(w, r)
}

// elsewhere is the location this forge points at: the same request, under
// another owner. A request that names none is pointed back at itself with a
// marker, so that a second hop is visible whatever was asked — and a request
// that already carries either is the second hop, which is served rather than
// moved again.
func (m *mover) elsewhere(uri string) (string, bool) {
	if strings.Contains(uri, "attacker") || strings.Contains(uri, "hop=1") {
		return "", false
	}
	if to := strings.Replace(uri, namespace, "attacker", 1); to != uri {
		return to, true
	}
	if strings.Contains(uri, "?") {
		return uri + "&hop=1", true
	}
	return uri + "?hop=1", true
}

// answer is the second hop, and everything on it succeeds: a driver that
// arrives here must not be able to tell it was somewhere else.
func (m *mover) answer(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(path, "/keys"), strings.HasSuffix(path, "/hooks"):
		moverJSON(w, `{"id":991}`)
	case strings.HasSuffix(path, "/installation"):
		moverJSON(w, `{"id":`+strconv.Itoa(installationID)+`}`)
	case strings.HasSuffix(path, "/access_tokens"):
		moverJSON(w, `{"token":"ghs_minted_for_the_walk"}`)
	case strings.Contains(path, "/memberships/"):
		moverJSON(w, `{"state":"active"}`)
	case strings.HasSuffix(path, "/api/v3/user"):
		// Not the key's owner, so Create takes the organization endpoint and
		// the POST it is really about is the request that gets moved.
		moverJSON(w, `{"login":"octo-tau","id":7}`)
	case strings.HasSuffix(path, "/user/repos"):
		// A short page, so the listing stops here rather than paging forever.
		moverJSON(w, "["+m.repository(r)+"]")
	default:
		moverJSON(w, m.repository(r))
	}
}

// repository is the body this forge answers with, and it describes THE
// REPOSITORY THAT WAS ASKED ABOUT even though it is served under attacker/. See
// the file comment: a successor that named itself would be caught by this
// driver's own key check instead of by the redirect rule.
func (m *mover) repository(r *http.Request) string {
	return `{"full_name":"` + namespace + `/pay",
		"clone_url":"https://` + r.Host + `/` + namespace + `/pay.git",
		"ssh_url":"ssh://git@` + r.Host + `/` + namespace + `/pay.git",
		"default_branch":"main","private":false,
		"permissions":{"admin":true,"maintain":true,"push":true,"pull":true}}`
}

func moverJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

func (m *mover) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.arrived...)
}

// movedOps is every call this driver makes, named, each returning what a
// consumer would have believed. `answered` names the requests the call makes
// BEFORE the one under test, so that the request a redirect is answered to is
// the operation's own — the create POST, the membership read — and not a
// prerequisite it shares with another row.
var movedOps = []struct {
	name     string
	answered []string
	call     func(context.Context, *forge.Registry, *forge.Session, repokey.Key) (any, error)
}{
	{name: "Whoami", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
		return s.Whoami(ctx)
	}},
	{name: "Access", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return s.Access(ctx, k)
	}},
	{name: "Repository", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		r, err := s.Repository(ctx, k)
		return r.Key, err
	}},
	{name: "ListRepositories", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
		var got []repokey.Key
		err := s.ListRepositories(ctx, func(r forge.Repository) bool { got = append(got, r.Key); return true })
		return got, err
	}},
	{
		name: "Create",
		// Create reads who the credential is before it can choose between the
		// user and the organization endpoint.
		answered: []string{"/api/v3/user"},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true})
			return r.Key, err
		},
	},
	{name: "Delete", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.Delete(ctx, k)
	}},
	{name: "InstallDeployKey", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return s.InstallDeployKey(ctx, k, "tau", "ssh-ed25519 AAAAC3Nz")
	}},
	{name: "RemoveDeployKey", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.RemoveDeployKey(ctx, k, forge.Handle("7"))
	}},
	{name: "RegisterPushHook", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		secret, err := forge.NewSecret()
		if err != nil {
			return nil, err
		}
		callback, err := url.Parse("https://build.example.invalid/hook")
		if err != nil {
			return nil, err
		}
		h, err := s.RegisterPushHook(ctx, k, callback, secret)
		return h.Handle, err
	}},
	{name: "RemovePushHook", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.RemovePushHook(ctx, k, forge.Handle("7"))
	}},
	{
		// The membership walk is three requests with the DEPLOYMENT's app
		// credential, and it is on the same client rule. This row moves the
		// last of the three — the read that decides the answer — so a followed
		// hop reports another namespace's membership as this one's.
		name:     "IsActiveMember",
		answered: []string{"/api/v3/orgs/" + namespace + "/installation", "/api/v3/app/installations/" + strconv.Itoa(installationID) + "/access_tokens"},
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), namespace, member)
		},
	},
	{
		// And the first of the three, because a policy could as easily follow
		// there: the installation lookup is what says this deployment's app is
		// on the namespace at all.
		name: "IsActiveMember at its installation lookup",
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), namespace, member)
		},
	},
}

// TestNoSameHostRedirectIsFollowed. A 3xx to another path on the SAME host is
// the hop a pin cannot refuse — it is the same host, and a per-hop host check is
// the whole of what a pin can decide — so every call this driver makes must stop
// at it rather than act on whatever the forge pointed at.
func TestNoSameHostRedirectIsFollowed(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,  // 301: Go rewrites the method to GET,
		http.StatusFound,             // 302: so a write reads a 200 and reports
		http.StatusSeeOther,          // 303: success having done nothing at all.
		http.StatusTemporaryRedirect, // 307: Go replays method AND body, so the
		http.StatusPermanentRedirect, // 308: write lands on the other repository.
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			for _, op := range movedOps {
				t.Run(op.name, func(t *testing.T) {
					m, reg, seen, sess, key := newMover(t, status, op.answered...)
					got, err := op.call(ctx(), reg, sess, key)
					sent := m.requests()
					t.Logf("%s -> %v, err = %v", op.name, got, err)
					t.Logf("the forge received %v", sent)

					require.Error(t, err,
						"%s followed a %d and answered as if the forge had spoken about %q",
						op.name, status, key)
					// "answered " and not the bare number: this driver renders a
					// failure as "METHOD path answered 301 Moved Permanently",
					// and a needle that is only digits is a needle any other
					// number in the sentence can satisfy.
					assert.Contains(t, err.Error(), "answered "+fmt.Sprint(status),
						"the error must name the 3xx: an operator whose repository was renamed has to be able to tell that from an outage")
					assert.Len(t, seen.urls, len(op.answered)+1,
						"the request was replayed at the location the forge chose, carrying this credential: %v", seen.urls)
				})
			}
		})
	}
}
