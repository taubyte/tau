package gitlab

// SAME-HOST REDIRECTS. Everything here is httptest and in-process: an instance
// that answers for one project by pointing at ANOTHER PROJECT ON ITSELF.

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

// successor is an instance whose FIRST answer is a 3xx to another path ON
// ITSELF, and which then serves that path as a perfectly working API.
//
// Both hops are the same host, so forge.PinnedTransport permits the second one
// and MUST: a per-hop host check is the whole of what a pin can decide. What is
// on the other end of that hop is another project entirely.
type successor struct {
	srv    *httptest.Server
	status int

	mu      sync.Mutex
	arrived []string
}

func newSuccessor(t *testing.T, status int) (*successor, *forge.Session, repokey.Key) {
	t.Helper()
	s := &successor{status: status}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)

	host := strings.TrimPrefix(s.srv.URL, "http://")
	drv, err := New(host, BaseURL(s.srv.URL))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse(s.srv.URL + "/victim/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(advToken))
	require.NoError(t, err)
	return s, sess, key
}

func (s *successor) serve(w http.ResponseWriter, r *http.Request) {
	// WITH the credential or without it is recorded, because that is what a
	// second hop costs: PinnedTransport attaches the header on every hop whose
	// host matches, and this location's does.
	arrival := r.Method + " " + r.URL.RequestURI()
	if r.Header.Get("PRIVATE-TOKEN") == advToken {
		arrival += " (carrying the credential)"
	}
	s.mu.Lock()
	s.arrived = append(s.arrived, arrival)
	first := len(s.arrived) == 1
	s.mu.Unlock()

	if first {
		// A renamed project, a transferred one, an instance behind a rewrite:
		// the answer names a location on the very host the credential is for.
		w.Header().Set("Location", s.elsewhere(r.URL.RequestURI()))
		w.WriteHeader(s.status)
		return
	}
	s.answer(w, r)
}

// elsewhere is the location the instance points at: the same request, under
// another namespace. A request that names no project is pointed back at itself
// with a marker, so that a second hop is visible whatever was asked.
func (s *successor) elsewhere(uri string) string {
	if moved := strings.Replace(uri, "victim", "successor", 1); moved != uri {
		return moved
	}
	if strings.Contains(uri, "?") {
		return uri + "&hop=1"
	}
	return uri + "?hop=1"
}

func (s *successor) project() map[string]any {
	return map[string]any{
		"path_with_namespace": "successor/pay",
		"http_url_to_repo":    s.srv.URL + "/successor/pay.git",
		"ssh_url_to_repo":     "ssh://git@" + strings.TrimPrefix(s.srv.URL, "http://") + "/successor/pay.git",
		"default_branch":      "main",
		"visibility":          "private",
	}
}

// answer is the second hop: everything succeeds, because a driver that arrives
// here must not be able to tell it was somewhere else.
func (s *successor) answer(w http.ResponseWriter, r *http.Request) {
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
		successorJSON(w, map[string]any{"id": 9, "username": "successor"})
	case strings.HasPrefix(path, "/api/v4/namespaces/"):
		successorJSON(w, map[string]any{"id": 42, "full_path": "victim"})
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

func (s *successor) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.arrived...)
}

// redirectOps is every call this driver makes, named. Each returns what a
// consumer would have believed.
var redirectOps = []struct {
	name string
	call func(context.Context, *forge.Session, repokey.Key) (any, error)
}{
	{"Whoami", func(ctx context.Context, s *forge.Session, _ repokey.Key) (any, error) {
		return s.Whoami(ctx)
	}},
	{"Access", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		return s.Access(ctx, k)
	}},
	{"Repository", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		r, err := s.Repository(ctx, k)
		return r.Key, err
	}},
	{"ListRepositories", func(ctx context.Context, s *forge.Session, _ repokey.Key) (any, error) {
		var got []repokey.Key
		err := s.ListRepositories(ctx, func(r forge.Repository) bool { got = append(got, r.Key); return true })
		return got, err
	}},
	{"Create", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		r, err := s.Create(ctx, k, forge.CreateOptions{Private: true})
		return r.Key, err
	}},
	{"Delete", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.Delete(ctx, k)
	}},
	{"InstallDeployKey", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		return s.InstallDeployKey(ctx, k, "tau", "ssh-ed25519 AAAAC3Nz")
	}},
	{"RemoveDeployKey", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.RemoveDeployKey(ctx, k, forge.Handle("5"))
	}},
	{"RegisterPushHook", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
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
	{"RemovePushHook", func(ctx context.Context, s *forge.Session, k repokey.Key) (any, error) {
		return nil, s.RemovePushHook(ctx, k, forge.Handle("5"))
	}},
}

// TestNoSameHostRedirectIsFollowed. A 3xx to another path on the SAME host is
// the hop a pin cannot refuse, and every call this driver makes must stop at it
// rather than act on whatever the instance pointed at.
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
					s, sess, key := newSuccessor(t, status)
					got, err := op.call(context.Background(), sess, key)
					sent := s.requests()
					t.Logf("%s -> %v, err = %v", op.name, got, err)
					t.Logf("the instance received %v", sent)

					require.Error(t, err,
						"%s followed a %d to %q and answered as if the instance had spoken about the repository tau asked for",
						op.name, status, s.elsewhere("/victim"))
					// "with " and not the bare number: this driver renders a
					// failure as `gitlab: <host> answered <method> <path> with
					// <status>`, and <host> is 127.0.0.1 and an ephemeral port
					// — which satisfies a needle of three digits often enough
					// to let a real regression through.
					assert.Contains(t, err.Error(), "with "+fmt.Sprint(status),
						"the error must name the 3xx: an operator whose project was renamed has to be able to tell that from an outage")
					assert.Len(t, sent, 1,
						"a second request was made, to a location the instance chose, carrying this credential: %v", sent)
				})
			}
		})
	}
}
