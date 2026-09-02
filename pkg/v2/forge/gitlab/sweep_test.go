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
// WHAT DISCOVERY STILL COULD NOT SEE, AND WHERE THAT IS ANSWERED. Running every
// operation finds every request some input reaches, and says nothing about a
// BRANCH no input reaches. That is not answered here, because it cannot be: no
// sample proves what a program can do. It is answered by the driver's ROUTING
// TABLE and the closure over it in route_test.go — a path is a route registered
// by being constructed, and a route the sweep never reached fails by name.
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
	"path"
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

// THE POPULATION IS IN TWO HALVES, AND IT USED TO BE IN ONE. A string a consumer
// or an instance hands this driver either can be escaped into a name — and then
// it must be sent, because an instance may legitimately spell a handle with a
// "%", a "?" or a "#" in it and refusing those would refuse real instances — or
// it cannot, and then it must be REFUSED before any request leaves. One string
// standing for both is one property standing for two, and the second one was
// missing: url.PathEscape("..") is "..", so the escaping sweep watched ".." go
// out escaped-and-unchanged and called that holding.
//
// walkingSpelling is the FIRST half: the characters escaping is the answer to.
// "?" ends the path, so an unescaped one turns the rest into query parameters
// this driver never wrote; "#" ends the URL, so an unescaped one silently
// TRUNCATES the request; "%2f" is a "/" the instance decodes after this driver
// has stopped looking. It is the same spelling walkingSegmentSpelt yields
// through repokey, so all five slots below carry ONE string and one needle
// decides all of them.
//
// traversalSpellings is the SECOND half, and it is asserted against a different
// property in a different test: not "it went out escaped" but "no request went
// out at all". See TestNoStringThisDriverPastesIntoAURLCanMoveThePathItIsIn.
const walkingSpelling = walkingSegment

// traversalSpellings are the strings that ADDRESS SOMETHING ELSE however
// perfectly they are escaped, which is the whole of why escaping was never the
// property.
//
// ".." resolves to the parent — DELETE .../projects/acme%2Fsite/deploy_keys/..
// is DELETE .../projects/acme%2Fsite, which is delete-a-project, and Go's own
// httptest server answered exactly that with a 307 to the cleaned path. "."
// resolves to the COLLECTION, which is the threat handleSegment was already
// written against. "../.." escapes to "..%2F..", one segment on the wire and two
// dot components to anything that decodes before it routes.
//
// EVERY SLOT TAKES THESE, because a dot segment is an instruction to a resolver
// wherever it is put and no instance has a name that is one.
var traversalSpellings = map[string]string{
	"a segment that climbs to the parent":         "..",
	"a segment that names the collection":         ".",
	"a climb spelled inside one escaped segment":  "../..",
	"a climb with the rest of a real path behind": "../../attacker?a#b",
}

// spanningSpellings carry NO DOT AT ALL and still name a resource under a
// collection nobody asked about: .../deploy_keys/a/b is not a key under
// .../deploy_keys. They are a slot's SHAPE rather than the resolver's
// arithmetic, so they are asked only of a slot that does not nest — see
// consumerSlot.nests. On THIS driver the membership group does nest: acme/team
// is the ordinary case, and asking this of it would be asking the driver to
// refuse GitLab's own addressing.
var spanningSpellings = map[string]string{
	"a name that spans two collections": "a/b",
}

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
)

// consumerSlot is one string a CONSUMER hands this driver, named by where it
// goes and paired with the args that put it there. It is what makes the
// traversal rows a population rather than three hand-written calls: the same
// three slots the escaping sweep runs over, driven by a different value.
type consumerSlot struct {
	name string
	// nests says a "/" in this slot is DATA rather than a second segment. It is
	// true for the membership group, where acme/team is GitLab's ordinary case,
	// and false everywhere else — which is why the spanning row is a property of
	// the SLOT and not of the string.
	nests bool
	args  func(value string) sweepArgs
}

var consumerSlots = []consumerSlot{
	{name: "a resource handle", args: func(v string) sweepArgs {
		return sweepArgs{handle: v, namespace: victim, username: memberName}
	}},
	{name: "a membership group", nests: true, args: func(v string) sweepArgs {
		return sweepArgs{handle: benignArgs.handle, namespace: v, username: memberName}
	}},
	{name: "a membership username", args: func(v string) sweepArgs {
		return sweepArgs{handle: benignArgs.handle, namespace: victim, username: v}
	}},
}

// spellingsFor is the population one slot is asked about: every dot spelling,
// and the spanning ones too unless a "/" is a name in this slot.
func spellingsFor(slot consumerSlot) map[string]string {
	out := map[string]string{}
	for name, spelling := range traversalSpellings {
		out[name] = spelling
	}
	if slot.nests {
		return out
	}
	for name, spelling := range spanningSpellings {
		out[name] = spelling
	}
	return out
}

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
	shape string
	// query is the encoded query this request carried. It is recorded apart
	// from the shape — a page counter is not a different endpoint — and pinned
	// on its own below, because it is still part of what was asked.
	query      string
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
		query:      r.URL.RawQuery,
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

// queries is every request that carried one, as "shape?query".
func (w *sweepWire) queries() []string {
	out := []string{}
	for _, h := range w.sent() {
		if h.query == "" {
			continue
		}
		out = append(out, h.shape+"?"+h.query)
	}
	return out
}

// parsedQuery is the parameters one request carried, refusing a query this
// driver could not have composed rather than reading it as none.
func parsedQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	require.NoErrorf(t, err, "this driver composed a query nothing can read: %q", raw)
	return v
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
	cfg      *config
	reg      *forge.Registry
	sess     *forge.Session
	key      repokey.Key
}

func newSweepInstance(t *testing.T) *sweepSubject {
	t.Helper()
	return newSweepInstanceNaming(t, "", "")
}

// newSweepInstanceNaming is the same subject with the session's key SPELLED
// differently in the clone URL it is derived from.
//
// The key is parsed rather than built, so a hostile spelling here is a key
// repokey really produces and not one a test imagined: whatever survives
// repokey's grammar is exactly the population this driver has to escape. The
// instance keeps describing the benign project, because what a hostile key is
// about is the REQUEST this driver composes from it.
func newSweepInstanceNaming(t *testing.T, ownerSpelling, nameSpelling string) *sweepSubject {
	t.Helper()
	in := &sweepInstance{}
	if ownerSpelling == "" {
		ownerSpelling = victim
	}
	if nameSpelling == "" {
		nameSpelling = "pay"
	}

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
	key, err := repokey.Parse(in.srv.URL + "/" + ownerSpelling + "/" + nameSpelling)
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(advToken))
	require.NoError(t, err)

	return &sweepSubject{instance: in, wire: w, cfg: cfgOf(drv), reg: reg, sess: sess, key: key}
}

// cfgOf is the driver's own configuration, which the route table is matched
// against. This file is inside the package, so it is a field and not a seam.
func cfgOf(drv forge.Driver) *config {
	switch d := drv.(type) {
	case *driver:
		return d.cfg
	case *memberDriver:
		return d.cfg
	}
	panic("gitlab: the sweep was given a driver this package did not build")
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
		// fullPath is the project that was ASKED about, so Access's own "did it
		// answer about something else" comparison is not what fails a redirect
		// row — the same reason the namespace answer below names itself.
		sweepJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{
			"fullPath":        victim + "/pay",
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
			"    fullPath\n" +
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

// sweptQueries is every request this driver composes that CARRIES a query,
// pinned by value.
//
// A path says which resource, a body says what to do to it, and a query says
// which slice of it — and until this list nothing read one. membership=true is
// the whole contract of the listing: without it the same endpoint answers every
// public project on the instance, which is a listing whose entries mean
// something else. per_page and the ordering are what make the pager
// deterministic, and username is how the membership walk names a human at all.
var sweptQueries = []string{
	"GET /api/v4/projects?membership=true&order_by=id&page=1&per_page=100&sort=asc",
	"GET /api/v4/users?username=" + memberName,
}

// TestTheSweepPinsEveryQueryThisDriverComposes.
func TestTheSweepPinsEveryQueryThisDriverComposes(t *testing.T) {
	found := []string{}
	for _, op := range sweepOps {
		s := newSweepInstance(t)
		_, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against an instance that answers everything correctly", op.name)
		found = append(found, s.wire.queries()...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	for _, q := range found {
		t.Log(q)
	}
	expect := append([]string(nil), sweptQueries...)
	slices.Sort(expect)
	assert.Equal(t, expect, found,
		"a query this driver composes changed; every parameter in one is a decision, so read the diff and say which decision this is")
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

// ───────────────────────── every string that reaches a URL ─────────────────────────

// A SLOT is one string this driver is handed and pastes into a URL, and the
// five below are the whole population. The github driver's lane says the same
// thing in the same place and for the same reason.
//
// WHAT COUNTS AS ONE IS WHERE THE STRING GOES, NOT WHERE IT CAME FROM. The
// previous mechanism swept three — an opaque handle and the two halves of a
// membership question — on the ground that they are "strings a consumer typed",
// as against a key "repokey vouched for and Addressable checked". Neither
// vouching is about a path: repokey splits on "/", refuses whitespace and
// refuses a segment that IS "." or "..", and says nothing about "?", "#" or
// "%"; Addressable here refuses a project at the host root and a segment of
// "-", and says nothing about the characters a URL gives meaning to. A consumer
// types the clone URL, so the two halves of a key are exactly as
// consumer-supplied as a handle is.
//
// This driver's API paths survived that gap because endpoint() escaped every
// segment it was given. THE ADDRESS IT HANDS BACK DID NOT: CloneAddress pasted
// key.FullName() into a clone URL raw, so a project spelled pay%23evil produced
// the address https://host/acme/pay#evil.git, which every clone tau ran with it
// would resolve to the project called pay. That is what running this property
// over composed URLs rather than over requests found.
type sweepSlot struct {
	name string

	// The run this slot is hostile in. A key slot is spelled into the clone URL
	// the session key is parsed from, so what the driver receives is a key
	// repokey really produces.
	args          sweepArgs
	ownerSpelling string
	nameSpelling  string

	// value is what the slot holds once this layer is done with it, which is
	// the string that has to survive as one segment.
	value string
}

// walkingSegment is what a KEY can carry, and every character in it is one
// repokey's grammar lets through. "?" ends a path, so an unescaped one turns
// the rest into query parameters this driver never wrote; "#" ends a URL, so an
// unescaped one TRUNCATES the request; and "%2f" is a "/" the instance decodes
// after this driver has stopped looking, which is what makes ".." in front of
// it a walk into another project rather than a name with dots in it.
//
// It has no literal "/" because repokey splits on one, and a test that fed a
// key an impossible spelling would be arming a population that does not exist.
const walkingSegment = "..%2f..%2fattacker?a#b"

// walkingSegmentSpelt is how a clone URL writes that segment. The two are
// asserted to correspond in TestTheHostileKeyIsOneRepokeyReallyProduces rather
// than assumed.
const walkingSegmentSpelt = "..%252f..%252fattacker%3Fa%23b"

// keySweepSlots are the two halves of the KEY, which reach this driver through
// repokey rather than from a consumer directly.
func keySweepSlots() []sweepSlot {
	return []sweepSlot{
		{
			name: "the project's namespace", args: benignArgs,
			ownerSpelling: walkingSegmentSpelt, value: walkingSegment,
		},
		{
			name: "the project's name", args: benignArgs,
			nameSpelling: walkingSegmentSpelt, value: walkingSegment,
		},
	}
}

func sweepSlots() []sweepSlot {
	out := keySweepSlots()
	for _, slot := range consumerSlots {
		out = append(out, sweepSlot{name: slot.name, args: slot.args(walkingSpelling), value: walkingSpelling})
	}
	return out
}

// TestTheHostileKeyIsOneRepokeyReallyProduces. The two key slots above are
// worth nothing if the spelling they use is one no clone URL yields, so this is
// the row that says the population is real.
func TestTheHostileKeyIsOneRepokeyReallyProduces(t *testing.T) {
	k, err := repokey.Parse("https://h.example/" + walkingSegmentSpelt + "/" + walkingSegmentSpelt)
	require.NoError(t, err, "repokey refuses the spelling the escaping sweep is written against")
	t.Logf("key = %s", k)
	assert.Equal(t, walkingSegment, k.Owner)
	assert.Equal(t, walkingSegment, k.Name)

	drv, err := New("h.example")
	require.NoError(t, err)
	assert.NoError(t, drv.Addressable(k),
		"this driver refuses the key the escaping sweep is written against, so the sweep would prove nothing")
}

// composedURL is one URL this driver put a string into: a request it made, or
// an address it handed back. Both halves are here because a string can leave
// its place in either of them — a "?" that survived unescaped in a path becomes
// a query parameter, and a consumer string pasted into a query value can become
// a second parameter. That second half is not decoration on this driver: the
// membership walk asks about a username by putting it in a query and nowhere
// else.
type composedURL struct {
	// what is the method, or ADDRESS for a URL that was returned rather than
	// sent.
	what  string
	path  string // escaped
	query string // encoded
	// route is the route a request matched, and nil for an address. What a
	// route declares is what its query may name.
	route *route
}

func (c composedURL) String() string {
	if c.query == "" {
		return c.what + " " + c.path
	}
	return c.what + " " + c.path + "?" + c.query
}

// composed is every URL one operation put a string into, with each request
// bound to the route that produced it.
func composed(t *testing.T, hops []sweepHop, cfg *config, got any) []composedURL {
	t.Helper()
	out := []composedURL{}
	for _, h := range hops {
		method, path, found := strings.Cut(h.shape, " ")
		require.Truef(t, found, "%q is not a request shape", h.shape)
		matched := assertOneRoute(t, cfg, h.shape)
		var r *route
		if len(matched) == 1 {
			r = matched[0]
		}
		out = append(out, composedURL{what: method, path: path, query: h.query, route: r})
	}
	for _, addr := range addressesIn(got) {
		u, err := url.Parse(addr.URL)
		require.NoErrorf(t, err, "this driver handed back an address that is not a URL: %q", addr.URL)
		out = append(out, composedURL{what: "ADDRESS", path: u.EscapedPath(), query: u.RawQuery})
	}
	return out
}

func addressesIn(got any) []forge.Address {
	switch v := got.(type) {
	case forge.Address:
		return []forge.Address{v}
	case []forge.Address:
		return v
	}
	return nil
}

// decodedSegments is a composed path split into the segments an instance will
// see, each one decoded exactly once.
func decodedSegments(t *testing.T, path string) []string {
	t.Helper()
	out := []string{}
	for _, segment := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(segment)
		require.NoErrorf(t, err, "this driver composed a segment that is not an encoding: %q", segment)
		out = append(out, decoded)
	}
	return out
}

// TestEveryStringThisDriverPastesIntoAURLStaysInsideOnePathSegment is ONE
// MECHANISM over the whole population, and the population is discovered the
// same way the request shapes are: each slot is made hostile ON ITS OWN and
// every operation is run twice, so whether a slot reaches a given operation's
// URLs is READ rather than declared.
//
// THE ASSERTION IS THE SEGMENT AND NOT A NEEDLE. A path is split and each piece
// decoded once, which is what the instance on the other end does, and the
// hostile value must come back out of exactly that. Escaping it correctly is
// the only way that happens: pasted raw, "..%2f..%2fattacker?a#b" arrives as
// the segment "../../attacker" with a query and a fragment the driver never
// wrote — and it arrives with the SAME NUMBER OF SEGMENTS, which is why
// counting separators is not enough and was not enough.
//
// AND THE VERDICT IS UNIVERSAL, WHICH IT WAS NOT. It used to be one `held`
// boolean ORed over every URL an operation composed, so a URL that walked was
// EXCUSED BY A CORRECTLY-ESCAPED URL BESIDE IT. Measured: making the ssh half of
// CloneAddress escape the key TWICE — the exact hazard escapedSegments' own
// comment warns about — while the https half stayed right was GO_TEST_EXIT=0 on
// both drivers. An existential check cannot express a universal property.
//
// So the verdict is per-URL. Benign and hostile are paired by position, and every
// URL the hostile run composed that DIFFERS from the benign one in the same
// position must hold the string itself: a URL the string did not change is a URL
// it is not in, and one it did change is one it has to be found in.
func TestEveryStringThisDriverPastesIntoAURLStaysInsideOnePathSegment(t *testing.T) {
	for _, slot := range sweepSlots() {
		t.Run(slot.name, func(t *testing.T) {
			reached := []string{}

			for _, op := range sweepOps {
				t.Run(op.name, func(t *testing.T) {
					b := newSweepInstance(t)
					benignGot, benignErr := op.call(context.Background(), b.reg, b.sess, b.key, benignArgs)
					t.Logf("benign  %s -> %v, err = %v", op.name, benignGot, benignErr)
					benign := composed(t, b.wire.sent(), b.cfg, benignGot)

					s := newSweepInstanceNaming(t, slot.ownerSpelling, slot.nameSpelling)
					got, err := op.call(context.Background(), s.reg, s.sess, s.key, slot.args)
					t.Logf("hostile %s -> %v, err = %v", op.name, got, err)
					urls := composed(t, s.wire.sent(), s.cfg, got)
					t.Logf("the driver composed %v", urls)

					// FEWER IS FINE AND MORE IS NOT. A hostile value can make
					// this driver stop earlier than a benign one — Create's own
					// "the instance resolved a different namespace" refusal does
					// exactly that — and that is a refusal rather than a defect.
					// An EXTRA URL is a request a consumer's string caused.
					require.LessOrEqualf(t, len(urls), len(benign),
						"%s composed more URLs for a hostile %s than for a benign one", op.name, slot.name)

					changed := []composedURL{}
					for i, u := range urls {
						// A REQUEST STILL NAMES ONLY WHAT ITS ROUTE DECLARES.
						// That is "the string opened nothing of its own" said
						// against the driver's own routing rather than against a
						// benign twin.
						if u.what == "ADDRESS" {
							assert.Emptyf(t, u.query, "a clone address carries a query: %v", u)
						} else {
							require.NotNilf(t, u.route, "%v matched no route", u)
							for name := range parsedQuery(t, u.query) {
								assert.Containsf(t, u.route.params, name,
									"%v carries a query parameter %s %s does not declare, so a string walked out of where it was put",
									u, u.route.method, u.route.template)
							}
						}

						// A URL THE HOSTILE STRING DID NOT CHANGE IS A URL IT
						// IS NOT IN. Create's POST names no namespace in its
						// path, and the user lookup in the middle of the
						// membership walk names only a query value. Those are
						// not this row's business, and pairing by position is
						// what tells them from a URL the string reached.
						if i < len(benign) && u.String() == benign[i].String() {
							continue
						}
						changed = append(changed, u)

						// WHERE THE STRING HAS TO BE FOUND: inside one path
						// segment, or as one whole query value. CONTAINS rather
						// than EQUALS for the segment, because a clone address
						// appends ".git" to the last one — which is the one
						// thing this driver adds to a name after escaping it,
						// and adding it before would have been the bug.
						assert.Truef(t, holdsTheString(t, u, slot.value),
							"%s composed %v out of a hostile %s and it does not hold the string as one path segment or one whole query value, so the string was truncated, re-encoded, or spread across segments",
							op.name, u, slot.name)
					}

					if len(changed) == 0 {
						// This slot does not reach this operation's URLs, so
						// there is nothing here for it to have escaped. Some
						// operation must reach it, and that is asserted below.
						return
					}
					reached = append(reached, op.name)
				})
			}

			assert.NotEmptyf(t, reached,
				"no operation composed a URL out of %s, so nothing in this row was read", slot.name)
			t.Logf("%s reaches %v", slot.name, reached)
		})
	}
}

// holdsTheString is one composed URL carrying the value WHOLE: inside one path
// segment, or as one whole query value. It is the needle the sweep above is
// written against, lifted out of the loop so the verdict can be per-URL rather
// than per-operation.
func holdsTheString(t *testing.T, u composedURL, value string) bool {
	t.Helper()
	for _, segment := range decodedSegments(t, u.path) {
		if strings.Contains(segment, value) {
			return true
		}
	}
	for _, values := range parsedQuery(t, u.query) {
		if slices.Contains(values, value) {
			return true
		}
	}
	return false
}

// TestNoStringThisDriverPastesIntoAURLCanMoveThePathItIsIn is the OTHER HALF of
// the property, and the half that was missing. Escaping answers a string that
// can be made into a name; nothing answers a string that IS an instruction to
// the resolver, and this driver was sending those.
//
// MEASURED ON THE CLEAN TREE, both drivers, RemoveDeployKey and RemovePushHook:
//
//	DELETE /api/v4/projects/acme%2Fsite/deploy_keys/..
//	  path.Clean -> /api/v4/projects/acme%2Fsite
//	DELETE /api/v4/projects/acme%2Fsite/deploy_keys/.
//	  path.Clean -> /api/v4/projects/acme%2Fsite/deploy_keys
//
// The first is GitLab's delete-project endpoint and the second is the
// collection. Go's own httptest server answered the first with a 307 to the
// cleaned path, so this is not a claim about hypothetical proxies: it is what
// the wire carried.
//
// THE ASSERTION IS ABOUT EVERY URL AND NOT ONLY THE REFUSED ONE. What holds
// everywhere is that NOTHING THIS DRIVER COMPOSED CAN BE MOVED BY A RESOLVER — a
// fixed point under RFC 3986 dot-segment removal, in the escaped spelling an
// intermediary resolves and in the decoded spelling a server holds. The refusal
// is then asserted where the string really arrives, so no row passes by the
// string never having been read.
func TestNoStringThisDriverPastesIntoAURLCanMoveThePathItIsIn(t *testing.T) {
	for _, slot := range consumerSlots {
		t.Run(slot.name, func(t *testing.T) {
			for name, spelling := range spellingsFor(slot) {
				t.Run(name, func(t *testing.T) {
					refused := []string{}
					for _, op := range sweepOps {
						s := newSweepInstance(t)
						got, err := op.call(context.Background(), s.reg, s.sess, s.key, slot.args(spelling))
						t.Logf("%s -> %v, err = %v", op.name, got, err)

						for _, u := range composed(t, s.wire.sent(), s.cfg, got) {
							assertStaysPut(t, op.name, u)
						}
						if op.pastes && err != nil {
							refused = append(refused, op.name)
						}
					}
					assert.NotEmptyf(t, refused,
						"no operation refused %q in %s, so this row read nothing", spelling, slot.name)
					t.Logf("%q in %s is refused by %v", spelling, slot.name, refused)
				})
			}
		})
	}
}

// assertStaysPut is the RFC 3986 property, said about one composed URL: a
// resolver removing dot segments leaves it exactly where this driver put it.
//
// BOTH SPELLINGS, because the two resolvers in the chain read different ones.
// The escaped path is what a conforming proxy, gateway or origin resolves before
// anything is decoded. The decoded path is what a server holds after the one
// decode it performs, and it is the spelling that catches "../..", which escapes
// to "..%2F..": one segment on the wire, two dot components to anything that
// decodes before it routes.
func assertStaysPut(t *testing.T, op string, u composedURL) {
	t.Helper()
	decoded := strings.Join(decodedSegments(t, u.path), "/")
	for _, spelling := range []string{u.path, decoded} {
		assert.Equalf(t, spelling, path.Clean(spelling),
			"%s composed %v, and removing dot segments from %q moves it to %q: the request addresses something the caller never named",
			op, u, spelling, path.Clean(spelling))
	}
}
