package gitlab

// SAME-HOST REDIRECTS, FOR EVERY REQUEST THIS DRIVER MAKES. Everything here is
// httptest and in-process: an instance that answers for one project by pointing
// at ANOTHER PROJECT ON ITSELF.
//
// noRedirect is one rule on the client, so it holds for every request or for
// none — and a table that arms only SOME of them is a table that says so for
// only some of them. Measured before the membership rows below existed: a
// per-method policy returning nil from noRedirect when the hop's path contains
// "/members/" left this whole package at exit 0, while the same policy on the
// github driver failed five named subtests. An instance answering 302 on
// /api/v4/groups/victim/members/all/7 with a location of
// /api/v4/groups/successor/members/all/7 then makes IsActiveMember(victim,
// alice) return (true, nil) off the OTHER group's member row — with the
// DEPLOYMENT's credential re-attached on the second hop, because the pin
// attaches it to every hop whose host matches and this location's does.
//
// THE PREREQUISITE REQUESTS ARE ANSWERED STRAIGHT, which is what makes each row
// a statement about its OWN request. Three of this driver's operations are more
// than one round trip — Create resolves a namespace before it creates, the
// membership walk resolves a username to an id before it reads the member row —
// and an instance that moves the FIRST request it is given moves the lookup
// rather than the write. Each such operation therefore appears twice: once with
// the lookup answered, so the operation's own request is the moved one, and once
// with nothing answered, so the lookup is.
//
// THE SECOND HOP DESCRIBES THE PROJECT THAT WAS ASKED ABOUT. It is served under
// successor/, and it says it is victim/pay, for the reason the github driver's
// probe states in the same place: a successor that named ITSELF would be caught
// by whatever key or namespace comparison the driver happens to make — Create
// already makes one — and the row would then pass on that check instead of on
// the redirect rule. Naming the victim closes it: if the hop is followed, every
// call below succeeds and returns what a consumer would have believed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

const (
	// victim is the namespace tau asks about — the project's owner and, for the
	// membership walk, the group. successor is where the instance points.
	victim    = "victim"
	successor = "successor"

	// memberName is the human the membership walk asks about. This instance
	// resolves them to an id, and the walk's second request is addressed by it.
	memberName = "alice"
)

// successorInstance is an instance whose answer to the request under test is a
// 3xx to another path ON ITSELF, and which then serves that path as a perfectly
// working API.
//
// Both hops are the same host, so forge.PinnedTransport permits the second one
// and MUST: a per-hop host check is the whole of what a pin can decide. What is
// on the other end of that hop is another project, and another group, entirely.
type successorInstance struct {
	srv    *httptest.Server
	status int

	// answered are the paths this instance serves straight, because the
	// operation under test makes them BEFORE the request being moved. Without
	// them Create is stopped at its namespace lookup and the membership walk at
	// its user lookup, and the request each row exists to arm is never the one a
	// redirect is answered to — which is how a per-method policy walks between
	// the rows.
	answered map[string]bool

	mu      sync.Mutex
	arrived []string
}

func newSuccessor(t *testing.T, status int, answered ...string) (*successorInstance, *forge.Registry, *forge.Session, repokey.Key) {
	t.Helper()
	s := &successorInstance{status: status, answered: map[string]bool{}}
	for _, p := range answered {
		s.answered[p] = true
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)

	host := strings.TrimPrefix(s.srv.URL, "http://")
	// WITH the deployment credential, so this driver is a forge.Memberships and
	// the membership walk — the one call made with a credential no session
	// holds — is reachable from the rows below at all.
	drv, err := New(host, BaseURL(s.srv.URL), Deployment(forge.Token(deployToken)))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse(s.srv.URL + "/" + victim + "/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(advToken))
	require.NoError(t, err)
	return s, reg, sess, key
}

func (s *successorInstance) serve(w http.ResponseWriter, r *http.Request) {
	// WHICH credential arrived is recorded, because that is what a second hop
	// costs: PinnedTransport attaches the header on every hop whose host
	// matches, and this location's does. The membership rows spend the
	// deployment's, and a hop that re-attaches THAT is the worst of the two.
	arrival := r.Method + " " + r.URL.RequestURI()
	switch r.Header.Get("PRIVATE-TOKEN") {
	case advToken:
		arrival += " (carrying the caller's credential)"
	case deployToken:
		arrival += " (carrying the deployment's credential)"
	}
	s.mu.Lock()
	s.arrived = append(s.arrived, arrival)
	s.mu.Unlock()

	if to, moved := s.elsewhere(r.URL.RequestURI()); moved && !s.answered[r.URL.EscapedPath()] {
		// A renamed project, a transferred one, an instance behind a rewrite:
		// the answer names a location on the very host the credential is for.
		w.Header().Set("Location", to)
		w.WriteHeader(s.status)
		return
	}
	s.answer(w, r)
}

// elsewhere is the location the instance points at: the same request, under
// another namespace. A request that names none is pointed back at itself with a
// marker, so that a second hop is visible whatever was asked — and a request
// that already carries either is the second hop, which is served rather than
// moved again.
func (s *successorInstance) elsewhere(uri string) (string, bool) {
	if strings.Contains(uri, successor) || strings.Contains(uri, "hop=1") {
		return "", false
	}
	if to := strings.Replace(uri, victim, successor, 1); to != uri {
		return to, true
	}
	if strings.Contains(uri, "?") {
		return uri + "&hop=1", true
	}
	return uri + "?hop=1", true
}

// project is the body this instance answers with, and it describes THE PROJECT
// THAT WAS ASKED ABOUT even though it is served under successor/. See the file
// comment.
func (s *successorInstance) project() map[string]any {
	return map[string]any{
		"path_with_namespace": victim + "/pay",
		"http_url_to_repo":    s.srv.URL + "/" + victim + "/pay.git",
		"ssh_url_to_repo":     "ssh://git@" + strings.TrimPrefix(s.srv.URL, "http://") + "/" + victim + "/pay.git",
		"default_branch":      "main",
		"visibility":          "private",
	}
}

// answer is the second hop: everything succeeds, because a driver that arrives
// here must not be able to tell it was somewhere else.
func (s *successorInstance) answer(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/hooks"):
		successorJSON(w, map[string]any{"id": 991})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/deploy_keys"):
		successorJSON(w, map[string]any{"id": 77, "can_push": false})
	case path == "/api/graphql":
		successorJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{
			"userPermissions": map[string]any{"pushCode": true, "adminProject": true}}}})
	case path == "/api/v4/user":
		successorJSON(w, map[string]any{"id": 9, "username": successor})
	case path == "/api/v4/users":
		// The membership walk's first request. It resolves a username to the id
		// its second request is addressed by.
		successorJSON(w, []any{map[string]any{"id": 7, "state": "active"}})
	case strings.Contains(path, "/members/all/"):
		// An ACTIVE membership, under whatever group the request arrived for.
		// This is the answer a followed hop turns into a verdict about a group
		// nobody asked about.
		successorJSON(w, map[string]any{"access_level": 40, "state": "active"})
	case strings.HasPrefix(path, "/api/v4/namespaces/"):
		// full_path is the namespace that was ASKED for, so Create's own
		// "did it resolve to itself" comparison is not what fails the row.
		successorJSON(w, map[string]any{"id": 42, "full_path": victim})
	case path == "/api/v4/projects" && r.Method == http.MethodGet:
		successorJSON(w, []any{s.project()})
	default:
		successorJSON(w, s.project())
	}
}

func successorJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (s *successorInstance) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.arrived...)
}

// redirectOps is every request this driver makes, named, each returning what a
// consumer would have believed. `answered` names the requests the operation
// makes BEFORE the one under test, so the request a redirect is answered to is
// the row's own — the create POST, the member read — and not a lookup it shares
// with another row.
//
// THE MEMBERSHIP ROWS TAKE THE REGISTRY AND NOT THE SESSION, because that walk
// is answered with the deployment's credential on a client no session can reach:
// a redirect policy that lives on one client and not the other would be invisible
// from a session, and that is exactly the shape of the defect these two rows
// were added for.
var redirectOps = []struct {
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
		// Create resolves the namespace to an id before it can create in it, so
		// the lookup is answered here and the POST is the moved request.
		name:     "Create",
		answered: []string{"/api/v4/namespaces/" + victim},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true})
			return r.Key, err
		},
	},
	{
		// And the lookup itself, because a policy could as easily follow there:
		// a namespace resolved through a hop is a project created in a group
		// nobody named.
		name: "Create at its namespace lookup",
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
		return nil, s.RemoveDeployKey(ctx, k, forge.Handle("5"))
	}},
	{name: "RegisterPushHook", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		secret, err := forge.NewSecret()
		if err != nil {
			return nil, err
		}
		callback, err := url.Parse("https://tau.example/hook")
		if err != nil {
			return nil, err
		}
		h, err := s.RegisterPushHook(ctx, k, callback, secret)
		return h.Handle, err
	}},
	{name: "RemovePushHook", call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.RemovePushHook(ctx, k, forge.Handle("5"))
	}},
	{
		// The membership walk is two requests with the DEPLOYMENT's credential,
		// and it is on the same client rule. This row moves the second — the
		// read that decides the answer — so a followed hop reports another
		// group's member row as this group's.
		name:     "IsActiveMember",
		answered: []string{"/api/v4/users"},
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), victim, memberName)
		},
	},
	{
		// And the first of the two: the lookup that turns a username into the
		// id the member read is addressed by. A hop followed there answers the
		// walk about whichever account the instance chose to point at.
		name: "IsActiveMember at its user lookup",
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), victim, memberName)
		},
	},
}

// TestNoSameHostRedirectIsFollowed. A 3xx to another path on the SAME host is
// the hop a pin cannot refuse — it is the same host, and a per-hop host check is
// the whole of what a pin can decide — so every request this driver makes must
// stop at it rather than act on whatever the instance pointed at.
func TestNoSameHostRedirectIsFollowed(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,  // 301: Go rewrites the method to GET,
		http.StatusFound,             // 302: so a write reads a 200 and reports
		http.StatusSeeOther,          // 303: success having done nothing at all.
		http.StatusTemporaryRedirect, // 307: Go replays method AND body, so the
		http.StatusPermanentRedirect, // 308: write lands on the other project.
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			for _, op := range redirectOps {
				t.Run(op.name, func(t *testing.T) {
					s, reg, sess, key := newSuccessor(t, status, op.answered...)
					got, err := op.call(context.Background(), reg, sess, key)
					sent := s.requests()
					t.Logf("%s -> %v, err = %v", op.name, got, err)
					t.Logf("the instance received %v", sent)

					require.Error(t, err,
						"%s followed a %d into %q and answered as if the instance had spoken about what tau asked for",
						op.name, status, successor)
					// "with " and not the bare number: this driver renders a
					// failure as `gitlab: <host> answered <method> <path> with
					// <status>`, and <host> is 127.0.0.1 and an ephemeral port
					// — which satisfies a needle of three digits often enough
					// to let a real regression through.
					assert.Contains(t, err.Error(), "with "+fmt.Sprint(status),
						"the error must name the 3xx: an operator whose project was renamed has to be able to tell that from an outage")
					assert.Len(t, sent, len(op.answered)+1,
						"the request was replayed at the location the instance chose, carrying a credential: %v", sent)
				})
			}
		})
	}
}
