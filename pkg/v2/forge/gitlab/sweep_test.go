package gitlab

// THE SWEEP: every request this driver issues, DISCOVERED rather than listed.
//
// The github driver's lane says why in full, and the two files are deliberately
// the same shape — a rule two drivers hold must be tested the same way, or the
// difference between them is a difference nobody can read. In short: a table
// that lists request shapes lists the ones somebody thought of, and five rounds
// of that produced a hole every round. So every operation is run once against an
// instance that answers everything correctly, what it put on the wire is read off
// the recorder UNDER the credential pin, and the properties then run over one
// (operation, request) pair at a time. The unit is a SITE IN A SEQUENCE, which is
// what arms the second request of a two-request operation on its own — the
// namespace lookup Create makes before it creates, the user lookup the membership
// walk makes before it reads a member row.
//
// THE SECOND HOP DESCRIBES THE PROJECT THAT WAS ASKED ABOUT even though it is
// served under successor/. A successor that named ITSELF would be caught by
// whatever key or namespace comparison the driver happens to make — Create makes
// one — and the row would then pass on that check instead of on the redirect
// rule. Naming the victim closes it: if a hop is followed, the operation succeeds
// and returns what a consumer would have believed.
//
// Everything here is httptest and in-process. Nothing dials anything that is not
// a loopback listener this file started.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
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

// redirectStatuses are the five an instance answers a move with, and they are
// two different defects. Go rewrites a non-HEAD request to GET on 301, 302 and
// 303 — so a followed write reads a 200 and reports success having done nothing
// — and replays method AND body on 307 and 308, so the write lands on whatever
// the instance named.
var redirectStatuses = []int{
	http.StatusMovedPermanently,
	http.StatusFound,
	http.StatusSeeOther,
	http.StatusTemporaryRedirect,
	http.StatusPermanentRedirect,
}

// sweepArgs are the strings a CONSUMER hands this driver, as opposed to the ones
// repokey vouched for and Addressable checked: an opaque forge.Handle and the two
// halves of a membership question.
type sweepArgs struct{ handle, namespace, username string }

// walkingSpelling is ONE string standing in for every consumer-supplied string,
// and it carries all three characters a URL gives meaning to. "/" opens a
// segment — and on GitLab that is not hypothetical decoration, because a project
// is addressed by its full path URL-ENCODED, so an unescaped "/" here is a
// request against a different route entirely. "?" ends the path, so an unescaped
// one turns the rest into query parameters this driver never wrote. "#" ends the
// URL, so an unescaped one silently TRUNCATES the request: removing the handle
// "1#x" removes the resource numbered 1.
//
// Because it is one string, one needle decides all three: escaped exactly once,
// it appears on the wire as url.PathEscape spells it, and only if every one of
// the three survived as itself.
const walkingSpelling = "../../attacker?a#b"

const (
	// sweepCallback and sweepSecret are FIXED rather than minted, because the
	// body sweep below pins what this driver composes by value and a freshly
	// minted secret derives a different wire string every run. Nothing about
	// arbitrary key material is given up here: the conformance suite signs real
	// deliveries with real minted secrets, and this file is about the request.
	sweepCallback = "https://tau.example/hook"
	sweepSecret   = "gitlab-sweep-hook-secret-0123456789abcdef"

	// sweepDescription is a create option that is neither empty nor the name, so
	// a driver that dropped it or sent something else in its place is visible in
	// the body sweep.
	sweepDescription = "the project tau was asked to make"
)

var (
	benignArgs = sweepArgs{handle: "5", namespace: victim, username: memberName}

	walkingArgs = sweepArgs{
		handle:    walkingSpelling,
		namespace: walkingSpelling,
		username:  walkingSpelling,
	}
)

// sweepOp is one operation a consumer can ask a session for. This list is the
// only hand-written thing in the file, it is closed against
// forge.Session.Capabilities by TestEveryCapabilityThisDriverAnswersIsSwept, and
// it names no endpoint: which requests an operation makes is discovered.
type sweepOp struct {
	name string
	caps []forge.Capability

	// pastes says this operation hands one of its sweepArgs to the driver, so
	// the escaping sweep can tell "the hostile value was escaped" from "the
	// hostile value never reached the wire".
	pastes bool

	// offline says this operation answers from the key alone. A request from one
	// is a defect of its own, and the sweep asserts there is none.
	offline bool

	// deployment says this operation is answered with the DEPLOYMENT's own
	// credential, on a client no session can reach. Whose credential a request
	// spends is the whole point of that placement, so the rows about a session's
	// credential must not quietly include it.
	deployment bool

	call func(context.Context, *forge.Registry, *forge.Session, repokey.Key, sweepArgs) (any, error)
}

var sweepOps = []sweepOp{
	{
		name: "Whoami", caps: []forge.Capability{forge.CapIdentity},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, _ repokey.Key, _ sweepArgs) (any, error) {
			return s.Whoami(ctx)
		},
	},
	{
		// Not REST: on GitLab the authorization question is a GraphQL POST, and
		// it is the one request this driver makes that is not under /api/v4.
		name: "Access", caps: []forge.Capability{forge.CapAccess},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			return s.Access(ctx, k)
		},
	},
	{
		name: "Repository", caps: []forge.Capability{forge.CapRepository},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			r, err := s.Repository(ctx, k)
			return r.Key, err
		},
	},
	{
		name: "ListRepositories", caps: []forge.Capability{forge.CapList},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, _ repokey.Key, _ sweepArgs) (any, error) {
			var got []repokey.Key
			err := s.ListRepositories(ctx, func(r forge.Repository) bool { got = append(got, r.Key); return true })
			return got, err
		},
	},
	{
		// Two requests: a project is created under a NAMESPACE ID, so the
		// namespace is resolved first. The sweep arms both of them, because a
		// namespace resolved through a hop is a project created in a group nobody
		// named.
		name: "Create", caps: []forge.Capability{forge.CapCreate},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true, Description: sweepDescription})
			return r.Key, err
		},
	},
	{
		// AND THE OTHER SIDE OF THE ONE BRANCH IN THAT BODY. GitLab has no
		// boolean here: forge.CreateOptions.Private is mapped onto one of two
		// visibility strings, so a driver that always sends "private" cannot make
		// the public project tau asked for and one that always sends "public"
		// publishes a configuration repository. Both values have to be on the
		// wire for either to be a fact.
		name: "Create a public project", caps: []forge.Capability{forge.CapCreate},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: false, Description: sweepDescription})
			return r.Key, err
		},
	},
	{
		name: "Delete", caps: []forge.Capability{forge.CapDelete},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			return nil, s.Delete(ctx, k)
		},
	},
	{
		name: "InstallDeployKey", caps: []forge.Capability{forge.CapDeployKeys},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			return s.InstallDeployKey(ctx, k, "tau", "ssh-ed25519 AAAAC3Nz")
		},
	},
	{
		name: "RemoveDeployKey", caps: []forge.Capability{forge.CapDeployKeys}, pastes: true,
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, a sweepArgs) (any, error) {
			return nil, s.RemoveDeployKey(ctx, k, forge.Handle(a.handle))
		},
	},
	{
		name: "RegisterPushHook", caps: []forge.Capability{forge.CapPushHooks, forge.CapSignatures},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			callback, err := url.Parse(sweepCallback)
			if err != nil {
				return nil, err
			}
			h, err := s.RegisterPushHook(ctx, k, callback, forge.SecretFrom([]byte(sweepSecret)))
			return h.Handle, err
		},
	},
	{
		name: "RemovePushHook", caps: []forge.Capability{forge.CapPushHooks}, pastes: true,
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, a sweepArgs) (any, error) {
			return nil, s.RemovePushHook(ctx, k, forge.Handle(a.handle))
		},
	},
	{
		// THE ONE CALL MADE WITH THE DEPLOYMENT'S OWN CREDENTIAL, on a client no
		// session can reach — which is why it is taken through the registry. A
		// redirect policy living on one of this driver's two http.Clients and not
		// the other would be invisible from a session, and that is exactly the
		// shape of the defect round 4 found here.
		name: "IsActiveMember", caps: []forge.Capability{forge.CapMembership}, pastes: true, deployment: true,
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key, a sweepArgs) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), a.namespace, a.username)
		},
	},
	{
		// It answers from the key alone: no context, no request, no credential.
		name: "CloneAddress", caps: []forge.Capability{forge.CapCloneAddr}, offline: true,
		call: func(_ context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			https, err := s.CloneAddress(k, forge.HTTPS)
			if err != nil {
				return nil, err
			}
			ssh, err := s.CloneAddress(k, forge.SSH)
			return []forge.Address{https, ssh}, err
		},
	},
}

// ─────────────────────────── what went on the wire ───────────────────────────

// sweepHop is one request this driver made, as the recorder UNDER the credential
// pin saw it — so the header here is the one the pin attached, on the hop it
// attached it to, and a request replayed at a second address is a second hop
// whatever address that was.
type sweepHop struct {
	shape      string
	credential string
	// carried is whether a credential header was THERE, which is not the same
	// question as whether it had anything in it: an empty PRIVATE-TOKEN is a
	// header, and reading the value cannot tell the two apart.
	carried bool
	body    string
}

// sweepWire records every hop, in order, and answers through the transport
// beneath it. A request the pin refuses never reaches it, which is the point:
// that is a request this driver asked to have made.
type sweepWire struct {
	base http.RoundTripper

	mu   sync.Mutex
	hops []sweepHop
}

func (w *sweepWire) RoundTrip(r *http.Request) (*http.Response, error) {
	w.mu.Lock()
	_, private := r.Header["Private-Token"]
	_, bearer := r.Header["Authorization"]
	w.hops = append(w.hops, sweepHop{
		shape:      sweepShapeOf(r),
		credential: r.Header.Get("PRIVATE-TOKEN"),
		carried:    private || bearer,
		body:       sweepBodyOf(r),
	})
	w.mu.Unlock()
	if w.base == nil {
		return http.DefaultTransport.RoundTrip(r)
	}
	return w.base.RoundTrip(r)
}

func (w *sweepWire) sent() []sweepHop {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]sweepHop(nil), w.hops...)
}

func (w *sweepWire) shapes() []string {
	out := []string{}
	for _, h := range w.sent() {
		out = append(out, h.shape)
	}
	return out
}

// bodies is every request that carried one, as "shape\n\tbody".
func (w *sweepWire) bodies() []string {
	out := []string{}
	for _, h := range w.sent() {
		if h.body == "" {
			continue
		}
		out = append(out, h.shape+"\n\t"+h.body)
	}
	return out
}

func (w *sweepWire) reset() {
	w.mu.Lock()
	w.hops = nil
	w.mu.Unlock()
}

// sweepShapeOf is a request shape: the method and the escaped path this driver
// composed. The query is deliberately absent — a page counter is not a different
// endpoint — and the path is the ESCAPED one, because a GitLab project is
// addressed by an encoded full path and whether a consumer's string stayed
// inside its segment is exactly what the escaping sweep reads here.
func sweepShapeOf(r *http.Request) string { return r.Method + " " + r.URL.EscapedPath() }

// maxSweepBody is larger than any body this driver composes and small enough
// that a bug producing an unbounded one is a failure rather than a hang.
const maxSweepBody = 1 << 20

// sweepBodyOf is what a request carried, read through GetBody so that reading it
// does not consume the one the driver is about to send. It is re-marshalled from
// a decode, so what the golden list below pins is the FIELDS AND VALUES and not
// the order a map happens to render them in.
func sweepBodyOf(r *http.Request) string {
	if r.GetBody == nil {
		return ""
	}
	rc, err := r.GetBody()
	if err != nil {
		return "<unreadable>"
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxSweepBody))
	if err != nil {
		return "<unreadable>"
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return strconv.Quote(string(raw))
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return strconv.Quote(string(raw))
	}
	return string(canonical)
}

// ─────────────────────────────── the instance ───────────────────────────────

// sweepInstance is a GitLab that answers everything correctly, and answers ONE
// request shape with a 3xx when it is armed to.
type sweepInstance struct {
	srv       *httptest.Server
	elsewhere *httptest.Server

	mu       sync.Mutex
	armed    string
	status   int
	offHost  bool
	fired    bool
	elsewhen []string
}

type sweepSubject struct {
	instance *sweepInstance
	wire     *sweepWire
	reg      *forge.Registry
	sess     *forge.Session
	key      repokey.Key
}

func newSweepInstance(t *testing.T) *sweepSubject {
	t.Helper()
	in := &sweepInstance{}

	// The off-host destination is a loopback listener on ANOTHER port, so
	// "another host" is a real host this file started rather than a name that
	// would have to be resolved. It records what reaches it, and nothing may.
	in.elsewhere = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		in.mu.Lock()
		in.elsewhen = append(in.elsewhen, sweepShapeOf(r))
		in.mu.Unlock()
	}))
	t.Cleanup(in.elsewhere.Close)

	in.srv = httptest.NewServer(http.HandlerFunc(in.serve))
	t.Cleanup(in.srv.Close)
	host := strings.TrimPrefix(in.srv.URL, "http://")

	w := &sweepWire{base: http.DefaultTransport}
	// WITH the deployment credential, so this driver is a forge.Memberships and
	// the membership walk — the one call made with a credential no session holds
	// — is reachable from the operations above at all.
	drv, err := New(host, BaseURL(in.srv.URL), HTTPTransport(w), Deployment(forge.Token(deployToken)))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse(in.srv.URL + "/" + victim + "/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(advToken))
	require.NoError(t, err)

	return &sweepSubject{instance: in, wire: w, reg: reg, sess: sess, key: key}
}

// arm points the next arrival of one shape somewhere else. offHost chooses which
// hop the row is about: a location on this very host, which forge.PinnedTransport
// permits and MUST — a per-hop host check is the whole of what a pin can decide —
// or one on the loopback listener next door, which it refuses one layer too late
// to be the thing that saves a write.
func (s *sweepSubject) arm(shape string, status int, offHost bool) {
	s.wire.reset()
	in := s.instance
	in.mu.Lock()
	in.armed, in.status, in.offHost, in.fired = shape, status, offHost, false
	in.elsewhen = nil
	in.mu.Unlock()
}

func (in *sweepInstance) serve(w http.ResponseWriter, r *http.Request) {
	shape := sweepShapeOf(r)

	in.mu.Lock()
	move := in.armed != "" && shape == in.armed && !in.fired
	if move {
		// ONCE. A location that is the same shape again — which is what a request
		// naming no namespace gets — would otherwise be a loop Go stops at ten
		// hops, and a row that passed on THAT would be saying nothing about this
		// driver.
		in.fired = true
	}
	status, offHost := in.status, in.offHost
	in.mu.Unlock()

	if move {
		http.Redirect(w, r, in.location(r, offHost), status)
		return
	}
	in.answer(w, r)
}

// location is where this instance points. On the same host it is the same
// request under another namespace, which is what a renamed or TRANSFERRED
// project answers and what re-registering the freed path turns into a grant; a
// request naming no namespace is pointed back at itself with a marker, so that a
// second hop is visible whatever was asked.
func (in *sweepInstance) location(r *http.Request, offHost bool) string {
	uri := r.URL.RequestURI()
	if offHost {
		return in.elsewhere.URL + uri
	}
	if to := strings.Replace(uri, victim, successor, 1); to != uri {
		return to
	}
	if strings.Contains(uri, "?") {
		return uri + "&hop=1"
	}
	return uri + "?hop=1"
}

// project is the body this instance answers with, and it describes THE PROJECT
// THAT WAS ASKED ABOUT even when it is served under successor/. See the file
// comment.
func (in *sweepInstance) project() map[string]any {
	return map[string]any{
		"path_with_namespace": victim + "/pay",
		"http_url_to_repo":    in.srv.URL + "/" + victim + "/pay.git",
		"ssh_url_to_repo":     "ssh://git@" + strings.TrimPrefix(in.srv.URL, "http://") + "/" + victim + "/pay.git",
		"default_branch":      "main",
		"visibility":          "private",
	}
}

// answer is the second hop: everything succeeds, because a driver that arrives
// here must not be able to tell it was somewhere else.
func (in *sweepInstance) answer(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/hooks"):
		sweepJSON(w, map[string]any{"id": 991})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/deploy_keys"):
		sweepJSON(w, map[string]any{"id": 77, "can_push": false})
	case path == "/api/graphql":
		sweepJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{
			"userPermissions": map[string]any{"pushCode": true, "adminProject": true}}}})
	case path == "/api/v4/user":
		sweepJSON(w, map[string]any{"id": 9, "username": successor})
	case path == "/api/v4/users":
		// The membership walk's first request. It resolves a username to the id
		// its second request is addressed by.
		sweepJSON(w, []any{map[string]any{"id": 7, "state": "active"}})
	case strings.Contains(path, "/members/all/"):
		// An ACTIVE membership, under whatever group the request arrived for.
		// This is the answer a followed hop turns into a verdict about a group
		// nobody asked about.
		sweepJSON(w, map[string]any{"access_level": 40, "state": "active"})
	case strings.HasPrefix(path, "/api/v4/namespaces/"):
		// full_path is the namespace that was ASKED for, so Create's own "did it
		// resolve to itself" comparison is not what fails the row.
		sweepJSON(w, map[string]any{"id": 42, "full_path": victim})
	case path == "/api/v4/projects" && r.Method == http.MethodGet:
		sweepJSON(w, []any{in.project()})
	default:
		sweepJSON(w, in.project())
	}
}

func sweepJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (in *sweepInstance) moved() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.fired
}

func (in *sweepInstance) reachedElsewhere() []string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]string(nil), in.elsewhen...)
}

// ─────────────────────────────── discovery ───────────────────────────────

// sweepShapes runs every operation against an instance that answers everything
// correctly and reports, per operation, the ordered shapes it put on the wire. It
// is the population every property below runs over, and it is read off the driver
// rather than written down.
func sweepShapes(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, op := range sweepOps {
		s := newSweepInstance(t)
		got, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err,
			"%s failed against an instance that answers everything correctly, so what it sends was never discovered and every property below skips it (it returned %v)",
			op.name, got)
		out[op.name] = s.wire.shapes()
		if op.offline {
			assert.Emptyf(t, out[op.name], "%s answers from the key alone, and it made a request: %v", op.name, out[op.name])
			continue
		}
		require.NotEmptyf(t, out[op.name], "%s made no request, so nothing about it is armed", op.name)
	}
	return out
}

// sweepPopulation is the distinct shapes, sorted.
func sweepPopulation(shapes map[string][]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range shapes {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	slices.Sort(out)
	return out
}

// sweptShapes is what the sweep found last time it was read, and it is here so
// that a REQUEST APPEARING OR VANISHING is a failure somebody has to look at
// rather than a silent change in what is armed. It is not a list of endpoints
// this file believes in: everything below is driven from the discovered set, so a
// new endpoint is swept whether or not it is written here — this is the line that
// makes it also VISIBLE.
var sweptShapes = []string{
	"DELETE /api/v4/projects/victim%2Fpay",
	"DELETE /api/v4/projects/victim%2Fpay/deploy_keys/5",
	"DELETE /api/v4/projects/victim%2Fpay/hooks/5",
	"GET /api/v4/groups/victim/members/all/7",
	"GET /api/v4/namespaces/victim",
	"GET /api/v4/projects",
	"GET /api/v4/projects/victim%2Fpay",
	"GET /api/v4/user",
	"GET /api/v4/users",
	"POST /api/graphql",
	"POST /api/v4/projects",
	"POST /api/v4/projects/victim%2Fpay/deploy_keys",
	"POST /api/v4/projects/victim%2Fpay/hooks",
}

// TestTheSweepDiscoversEveryRequestShapeThisDriverIssues.
func TestTheSweepDiscoversEveryRequestShapeThisDriverIssues(t *testing.T) {
	shapes := sweepShapes(t)
	found := sweepPopulation(shapes)
	for _, op := range sweepOps {
		t.Logf("%-20s %v", op.name, shapes[op.name])
	}
	assert.Equal(t, sweptShapes, found,
		"the requests this driver makes changed; every property in this file runs over the discovered set, so read the diff and say whether the new one is armed the way the others are")
}

// sweptBodies is every request this driver composes that CARRIES a body, pinned
// by value.
//
// A path says which resource a request is about; a body says what it asks to be
// done to it. Measured: enable_ssl_verification could be flipped to false and
// each of four event flags turned on, one at a time, with the whole tree at exit
// 0 — and the same held for the create body's visibility and description. The
// hook body now has a test of its own as well, because that one is the whole
// authentication of a delivery; this list is what makes the OTHER bodies facts.
//
// IT IS THE WHOLE BODY AND NOT A FIELD LIST: a field ADDED here is a value
// nobody decided, and only equality fails on one. The GraphQL query is in it for
// the same reason — Access is the authorization question, and which fields it
// asks for is the answer's meaning.
var sweptBodies = []string{
	// THE QUERY IS SPELLED OUT rather than referred to. Writing accessQuery here
	// would compare the driver with itself, and a golden that moves with what it
	// pins pins nothing: dropping adminProject from the query — which is the
	// difference between AccessAdmin and AccessWrite, and so between "may install
	// a hook" and "may not" — left the whole tree at exit 0 while this list said
	// accessQuery.
	"POST /api/graphql\n\t" + sweepJSONOf(map[string]any{
		"query": "query($fullPath: ID!) {\n" +
			"  project(fullPath: $fullPath) {\n" +
			"    userPermissions { pushCode adminProject }\n" +
			"  }\n" +
			"}",
		"variables": map[string]any{"fullPath": victim + "/pay"},
	}),
	"POST /api/v4/projects\n\t" + sweepJSONOf(map[string]any{
		"path": "pay", "name": "pay", "namespace_id": 42,
		"visibility": "private", "description": sweepDescription,
		"initialize_with_readme": false,
	}),
	"POST /api/v4/projects\n\t" + sweepJSONOf(map[string]any{
		"path": "pay", "name": "pay", "namespace_id": 42,
		"visibility": "public", "description": sweepDescription,
		"initialize_with_readme": false,
	}),
	"POST /api/v4/projects/victim%2Fpay/deploy_keys\n\t" + sweepJSONOf(map[string]any{
		"title": "tau", "key": "ssh-ed25519 AAAAC3Nz", "can_push": false,
	}),
	"POST /api/v4/projects/victim%2Fpay/hooks\n\t" + sweepJSONOf(map[string]any{
		"url": sweepCallback, "push_events": true, "tag_push_events": false,
		"enable_ssl_verification": true, "merge_requests_events": false,
		"issues_events": false, "pipeline_events": false,
		"repository_update_events": false, "token": sweepHookWire(),
	}),
}

// sweepJSONOf renders an expected body the same way sweepBodyOf renders a real
// one, so the two are compared as values rather than as spellings.
func sweepJSONOf(v map[string]any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// sweepHookWire is the string the core derives from sweepSecret under this
// driver's default scheme. It is derived rather than written down, because a
// driver that mangled the wire string would otherwise be pinned to its own
// mangling.
func sweepHookWire() string {
	wire, err := forge.SchemeSharedToken.Wire(forge.SecretFrom([]byte(sweepSecret)))
	if err != nil {
		panic(err)
	}
	return wire
}

// TestTheSweepPinsEveryRequestBodyThisDriverComposes.
func TestTheSweepPinsEveryRequestBodyThisDriverComposes(t *testing.T) {
	found := []string{}
	for _, op := range sweepOps {
		s := newSweepInstance(t)
		_, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against an instance that answers everything correctly", op.name)
		found = append(found, s.wire.bodies()...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	for _, b := range found {
		t.Log(b)
	}
	expect := append([]string(nil), sweptBodies...)
	slices.Sort(expect)
	assert.Equal(t, expect, found,
		"a request body this driver composes changed; every field in one is a decision, so read the diff and say which decision this is")
}

// TestEveryCapabilityThisDriverAnswersIsSwept is the closure on the one
// hand-written list here. A face this driver grows is a capability
// forge.Session.Capabilities computes from a type assertion, and an operation
// nobody added for it would be a question the sweep never asks.
func TestEveryCapabilityThisDriverAnswersIsSwept(t *testing.T) {
	s := newSweepInstance(t)

	swept := forge.Set{}
	for _, op := range sweepOps {
		for _, c := range op.caps {
			swept[c] = struct{}{}
		}
	}
	assert.Equal(t, s.sess.Capabilities().List(), swept.List(),
		"the sweep's operations and this driver's capabilities are not the same set")
}

// ─────────────────────────────── the properties ───────────────────────────────

// sweepRows runs fn over every (operation, request) pair the sweep discovered.
// The unit is a SITE IN A SEQUENCE: index is how many requests the operation
// makes before this one, which is also the hop budget a refusal has to keep to.
//
// AND IT CHECKS ITSELF. The rows are generated from the discovered set, so a
// shape cannot go unarmed by being forgotten — but it could go unarmed by
// something here skipping it, which is the same hole with a different cause. So
// what was actually armed is collected and compared with what was discovered.
func sweepRows(t *testing.T, fn func(t *testing.T, op sweepOp, index int, shape string)) {
	t.Helper()
	shapes := sweepShapes(t)
	armed := map[string]bool{}
	for _, op := range sweepOps {
		if op.offline {
			continue
		}
		for i, shape := range shapes[op.name] {
			armed[shape] = true
			// The shape is in the subtest name so a failure names the request and
			// not a number. Its slashes would nest the name, so they are spelled
			// with dots.
			name := fmt.Sprintf("%s/%d %s", op.name, i+1, strings.ReplaceAll(shape, "/", "."))
			t.Run(name, func(t *testing.T) { fn(t, op, i, shape) })
		}
	}
	covered := make([]string, 0, len(armed))
	for shape := range armed {
		covered = append(covered, shape)
	}
	slices.Sort(covered)
	assert.Equal(t, sweepPopulation(shapes), covered,
		"a request this driver makes was armed by no row, so this property holds for the others and says nothing about it")
}

// TestNoRequestThisDriverMakesFollowsASameHostRedirect. A 3xx to another path on
// the SAME host is the hop a pin cannot refuse, so every request this driver
// makes must stop at it rather than act on whatever the instance pointed at.
func TestNoRequestThisDriverMakesFollowsASameHostRedirect(t *testing.T) {
	sweepRows(t, func(t *testing.T, op sweepOp, index int, shape string) {
		s := newSweepInstance(t)
		for _, status := range redirectStatuses {
			t.Run(strconv.Itoa(status), func(t *testing.T) {
				s.arm(shape, status, false)
				got, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
				sent := s.wire.sent()
				t.Logf("%s -> %v, err = %v", op.name, got, err)
				t.Logf("the driver made %v", sent)

				require.Truef(t, s.instance.moved(),
					"%q never arrived, so this row moved nothing and says nothing", shape)
				require.Errorf(t, err,
					"%s followed a %d on %q into %q and answered as if the instance had spoken about what tau asked for",
					op.name, status, shape, successor)
				// "with " and not the bare number: this driver renders a failure
				// as `gitlab: <host> answered <method> <path> with <status>`, and
				// <host> is 127.0.0.1 and an ephemeral port — which satisfies a
				// needle of three digits often enough to let a regression through.
				assert.Contains(t, err.Error(), "with "+strconv.Itoa(status),
					"the error must name the 3xx: an operator whose project was renamed has to be able to tell that from an outage")
				require.Lenf(t, sent, index+1,
					"the request was replayed at the location the instance chose, carrying a credential: %v", sent)
				assert.Equal(t, shape, sent[index].shape, "the moved request is not the last one this driver made")
				for _, h := range sent {
					assert.NotEmptyf(t, h.credential,
						"nothing attached a credential to %q, so a hop that carried one would prove nothing", h.shape)
				}
			})
		}
	})
}

// TestNoRequestThisDriverMakesFollowsARedirectOffTheHost is the other half, and
// the assertion that the refusal is the DRIVER's rather than the pin's: the pin
// would have refused this hop, but only after being asked to make it, and by then
// a 307 has already replayed the body once for every host that answers.
func TestNoRequestThisDriverMakesFollowsARedirectOffTheHost(t *testing.T) {
	sweepRows(t, func(t *testing.T, op sweepOp, index int, shape string) {
		s := newSweepInstance(t)
		for _, status := range redirectStatuses {
			t.Run(strconv.Itoa(status), func(t *testing.T) {
				s.arm(shape, status, true)
				got, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
				sent := s.wire.sent()
				t.Logf("%s -> %v, err = %v", op.name, got, err)

				require.Truef(t, s.instance.moved(), "%q never arrived, so this row moved nothing", shape)
				require.Errorf(t, err, "%s followed a %d off the host on %q", op.name, status, shape)
				assert.Contains(t, err.Error(), "with "+strconv.Itoa(status))
				assert.Empty(t, s.instance.reachedElsewhere(),
					"a request this driver built reached another host")
				require.Lenf(t, sent, index+1, "the request was replayed off the host: %v", sent)

				var leak *forge.CredentialLeakError
				assert.False(t, errors.As(err, &leak),
					"the driver asked the pin to make the second request instead of refusing to make one")
				assert.NotContains(t, err.Error(), advToken)
				assert.NotContains(t, err.Error(), deployToken)
			})
		}
	})
}

// TestAnAnonymousSessionPutsNoCredentialHeaderOnAnyRequest.
//
// An anonymous session is a real state — GitLab answers public projects to
// nobody in particular — and the driver's own line for it is an early return
// before the header is set. Deleting that guard left the whole tree at exit 0,
// because the assertion an anonymous test can most easily make reads the
// header's VALUE, and an empty PRIVATE-TOKEN is empty by that reading and
// present by the instance's.
//
// So this reads presence, and it reads it on every request the sweep found
// rather than on one. Both spellings are checked, because which header carries
// the credential is a construction option here.
func TestAnAnonymousSessionPutsNoCredentialHeaderOnAnyRequest(t *testing.T) {
	reached := 0
	for _, op := range sweepOps {
		t.Run(op.name, func(t *testing.T) {
			s := newSweepInstance(t)
			anon, err := s.reg.For(s.key, forge.Anonymous())
			require.NoError(t, err)

			got, err := op.call(context.Background(), s.reg, anon, s.key, benignArgs)
			t.Logf("%s -> %v, err = %v", op.name, got, err)
			if op.deployment {
				// It never spends the session's credential, so an anonymous
				// session changes nothing about it — and THAT is this row's
				// assertion, because a walk that started answering anonymously
				// would be a walk asking a question it cannot answer.
				for _, h := range s.wire.sent() {
					assert.Truef(t, h.carried,
						"%s stopped spending the deployment's credential on %q", op.name, h.shape)
				}
				return
			}
			for _, h := range s.wire.sent() {
				reached++
				assert.Falsef(t, h.carried,
					"an anonymous session put a credential header on %q", h.shape)
			}
		})
	}
	// The core refuses some operations on an anonymous session before any
	// request is built — Whoami and Access both do — so this counts the hops
	// that WERE made, and a table where none of them reach the wire is a table
	// that read nothing.
	assert.Positive(t, reached, "no operation reached the wire, so nothing here was read")
}

// TestEveryConsumerStringStaysInsideOnePathSegment is ONE MECHANISM for the whole
// population, rather than a test per string somebody remembered.
//
// A forge.Handle is opaque — nothing in this package parses or orders one — and
// the two halves of a membership question are strings a consumer typed. None of
// the three is a key repokey vouched for or a shape Addressable checked, and they
// are pasted into API paths.
//
// THE ASSERTION IS THE SHAPE AND NOT A NEEDLE. Each operation runs twice, once
// with benign values and once with walking ones, and the requests must match
// segment for segment: an unescaped "/" is a segment that appeared, whichever
// parameter it came from and whether or not anybody wrote a case for it.
func TestEveryConsumerStringStaysInsideOnePathSegment(t *testing.T) {
	benign := sweepShapes(t)
	for _, op := range sweepOps {
		if op.offline {
			continue
		}
		t.Run(op.name, func(t *testing.T) {
			s := newSweepInstance(t)
			got, err := op.call(context.Background(), s.reg, s.sess, s.key, walkingArgs)
			t.Logf("%s -> %v, err = %v", op.name, got, err)
			sent := s.wire.shapes()
			t.Logf("the driver sent %v", sent)

			require.Lenf(t, sent, len(benign[op.name]),
				"%s made a different number of requests for a hostile argument than for a benign one", op.name)
			for i, shape := range sent {
				assert.Equalf(t, strings.Count(benign[op.name][i], "/"), strings.Count(shape, "/"),
					"a consumer's string opened %d path segment(s) of its own in %q",
					strings.Count(shape, "/")-strings.Count(benign[op.name][i], "/"), shape)
				assert.NotContainsf(t, shape, "../", "a consumer's string walked out of its path segment: %q", shape)
			}
			if !op.pastes {
				assert.Equal(t, benign[op.name], sent,
					"%s hands the driver none of these strings, so its requests must not have changed", op.name)
				return
			}
			assert.NotEqualf(t, benign[op.name], sent,
				"%s is marked as handing the driver a consumer's string and its requests did not change, so this row proves nothing", op.name)
			assert.Containsf(t, strings.Join(sent, " "), url.PathEscape(walkingSpelling),
				"%s put a consumer's string on the wire as something other than one escaped path segment, or never put it there at all", op.name)
		})
	}
}
