package github_test

// THE SWEEP: every request this driver issues, DISCOVERED rather than listed.
//
// Five rounds of a hand-written redirect table produced a hole every round, and
// each hole was the same shape — the table named OPERATIONS, so a multi-request
// operation was armed at one of its requests and unarmed at the others, and an
// operation nobody thought of was armed nowhere. Round 6 found the last two by
// sweeping all thirteen request shapes instead of reading the table. This file
// makes that sweep the test.
//
// HOW THE POPULATION IS FOUND. Every operation is run once against a forge that
// answers everything correctly, and what the driver put on the wire is read off
// the recorder UNDER the credential pin: a shape here is a method and the
// escaped path this driver composed, and nothing in this file may name an
// endpoint that did not arrive there. The properties below then run over one
// (operation, request) pair at a time — the population is a SITE IN A SEQUENCE,
// not an operation — so a request that is second or third in its operation is
// armed on its own, and an operation that grows a fourth request grows a row
// with it.
//
// WHAT THAT WOULD HAVE CAUGHT. POST /api/v3/user/repos — the endpoint Create
// takes when the key names the credential's own account — was armed by nothing
// for five rounds, because the one probe that reached Create answered
// /api/v3/user with a login that was never the key's owner, so the personal
// branch of client.go was never taken. Here BOTH branches are operations, and
// which endpoint each one reached is a fact this file reads rather than states.
//
// WHAT DISCOVERY STILL COULD NOT SEE, AND WHERE THAT IS ANSWERED. Running every
// operation finds every request some input reaches, and says nothing about a
// BRANCH no input reaches — which is the same hole as the personal create
// endpoint, one level in. Round 7 measured it: adding
//
//	if strings.HasPrefix(key.Name, "z") { path = "user/repos?legacy=1" }
//
// to an existing operation left the whole forge tree at exit 0. That is not
// answered here, because it cannot be: no sample proves what a program can do.
// It is answered by the driver's ROUTING TABLE and the closure over it in
// route_test.go — a path is a route registered by being constructed, and a
// route the sweep never reached fails by name.
//
// THE SECOND HOP DESCRIBES THE REPOSITORY THAT WAS ASKED ABOUT even though it
// is served under attacker/. This driver checks the key it derives from a clone
// URL against the key it asked about — Access and Create both do — so a
// successor that named ITSELF would make these rows pass on that check instead
// of on the redirect rule. Naming the victim closes it: if a hop is followed,
// the operation succeeds and returns what a consumer would have believed.
//
// Everything here is httptest and in-process. Nothing dials anything that is
// not a loopback listener this file started.

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

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// redirectStatuses are the five a forge answers a move with, and they are two
// different defects. Go rewrites a non-HEAD request to GET on 301, 302 and 303 —
// so a followed write reads a 200 and reports success having done nothing — and
// replays method AND body on 307 and 308, so the write lands on whatever the
// forge named.
var redirectStatuses = []int{
	http.StatusMovedPermanently,
	http.StatusFound,
	http.StatusSeeOther,
	http.StatusTemporaryRedirect,
	http.StatusPermanentRedirect,
}

// sweepArgs are the strings a CONSUMER hands this driver, as opposed to the
// ones repokey vouched for and Addressable checked: an opaque forge.Handle and
// the two halves of a membership question. Each operation below takes the whole
// struct and uses whichever of them it has a parameter for, which is what lets
// one table drive both the redirect sweep and the escaping sweep.
type sweepArgs struct{ handle, namespace, username string }

// THE POPULATION IS IN TWO HALVES, AND IT USED TO BE IN ONE. A string a
// consumer or a forge hands this driver either can be escaped into a name — and
// then it must be sent, because a forge may legitimately spell a handle with a
// "%", a "?" or a "#" in it and refusing those would refuse real forges — or it
// cannot, and then it must be REFUSED before any request leaves. One string
// standing for both is one property standing for two, and the second one was
// missing: url.PathEscape("..") is "..", so the escaping sweep watched ".." go
// out escaped-and-unchanged and called that holding.
//
// walkingSpelling is the FIRST half: the characters escaping is the answer to.
// "?" ends a path, so an unescaped one turns the rest into query parameters this
// driver never wrote; "#" ends a URL, so an unescaped one silently TRUNCATES the
// request; "%2f" is a "/" the forge decodes after this driver has stopped
// looking. It is the same spelling walkingSegmentSpelt yields through repokey,
// so all five slots below carry ONE string and one needle decides all of them.
//
// traversalSpellings is the SECOND half, and it is asserted against a different
// property in a different test: not "it went out escaped" but "no request went
// out at all". See TestNoStringThisDriverPastesIntoAURLCanMoveThePathItIsIn.
const walkingSpelling = walkingSegment

// traversalSpellings are the strings that ADDRESS SOMETHING ELSE however
// perfectly they are escaped, which is the whole of why escaping was never the
// property.
//
// ".." resolves to the parent — DELETE .../repos/acme/site/keys/.. is DELETE
// .../repos/acme/site, which is delete-a-repository, and Go's own httptest
// server answered exactly that with a 307 to the cleaned path. "." resolves to
// the COLLECTION, which is the threat handleSegment was already written
// against. "../.." escapes to "..%2F..", one segment on the wire and two dot
// components to anything that decodes before it routes.
//
// EVERY SLOT TAKES THESE, because a dot segment is an instruction to a resolver
// wherever it is put and no forge has a name that is one.
var traversalSpellings = map[string]string{
	"a segment that climbs to the parent":         "..",
	"a segment that names the collection":         ".",
	"a climb spelled inside one escaped segment":  "../..",
	"a climb with the rest of a real path behind": "../../attacker?a#b",
}

// spanningSpellings carry NO DOT AT ALL and still name a resource under a
// collection nobody asked about: .../keys/a/b is not a key under .../keys. They
// are a slot's SHAPE rather than the resolver's arithmetic, so they are asked
// only of a slot that does not nest — see consumerSlot.nests. On this driver
// nothing nests: Addressable already refuses a key whose owner does, and an
// organization login and a user login are the same shape.
var spanningSpellings = map[string]string{
	"a name that spans two collections": "a/b",
}

const (
	// sweepCallback and sweepSecret are FIXED rather than minted, because the
	// body sweep below pins what this driver composes by value and a freshly
	// minted secret derives a different wire string every run. Nothing about
	// arbitrary key material is given up here: the conformance suite signs real
	// deliveries with real minted secrets, and this file is about the request.
	sweepCallback = "https://build.example.invalid/hook"
	sweepSecret   = "github-sweep-hook-secret-0123456789abcdef"

	// sweepDescription is a create option that is neither empty nor the name, so
	// a driver that dropped it or sent something else in its place is visible in
	// the body sweep. It was dropped, and nothing said so.
	sweepDescription = "the repository tau was asked to make"
)

var (
	benignArgs = sweepArgs{handle: "7", namespace: namespace, username: member}
)

// consumerSlot is one string a CONSUMER hands this driver, named by where it
// goes and paired with the args that put it there. It is what makes the
// traversal rows a population rather than three hand-written calls: the same
// three slots the escaping sweep runs over, driven by a different value.
type consumerSlot struct {
	name string
	// nests says a "/" in this slot is DATA rather than a second segment. It is
	// false everywhere on this driver and true for the gitlab driver's group,
	// where acme/team is the ordinary case — which is why the spanning row is a
	// property of the SLOT and not of the string.
	nests bool
	args  func(value string) sweepArgs
}

var consumerSlots = []consumerSlot{
	{name: "a resource handle", args: func(v string) sweepArgs {
		return sweepArgs{handle: v, namespace: namespace, username: member}
	}},
	{name: "a membership namespace", args: func(v string) sweepArgs {
		return sweepArgs{handle: benignArgs.handle, namespace: v, username: member}
	}},
	{name: "a membership username", args: func(v string) sweepArgs {
		return sweepArgs{handle: benignArgs.handle, namespace: namespace, username: v}
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
// forge.Session.Capabilities by TestEveryCapabilityThisDriverAnswersIsSwept,
// and it names no endpoint: which requests an operation makes is discovered.
type sweepOp struct {
	name string
	caps []forge.Capability

	// owner is the namespace of the key this operation names. It matters for
	// exactly one row: Create branches on whether the key's owner IS the
	// credential's own login, and the two branches are two endpoints.
	owner string

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
		// The key's owner is an organization, so Create reads who the credential
		// is and then posts to /orgs/{owner}/repos.
		name: "Create under an organization", caps: []forge.Capability{forge.CapCreate},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true, Description: sweepDescription})
			return r.Key, err
		},
	},
	{
		// AND THE OTHER BRANCH. POST /orgs/{me}/repos is a 404 because a user is
		// not an organization, so a key naming the credential's own account
		// takes a different endpoint — the one that was armed by nothing for
		// five rounds, because the only probe that reached Create answered
		// /api/v3/user with a login the key's owner never was.
		// It also asks for a PUBLIC repository, so that between the two rows both
		// values of the option are on the wire: a driver that ignores Private is
		// a driver that publishes a configuration repository, and one that always
		// sends true is a driver that cannot make the public one tau was asked
		// for.
		name: "Create under the caller's own account", caps: []forge.Capability{forge.CapCreate},
		owner: login,
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
		name: "ListDeployKeys", caps: []forge.Capability{forge.CapDeployKeys},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			return s.ListDeployKeys(ctx, k)
		},
	},
	{
		name: "ListPushHooks", caps: []forge.Capability{forge.CapPushHooks},
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			return s.ListPushHooks(ctx, k)
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
		// session can reach: three requests, the middle of which is the only
		// request tau makes that HANDS BACK a credential. It is taken through the
		// registry for that reason — a redirect policy living on one client and
		// not the other would be invisible from a session.
		name: "IsActiveMember", caps: []forge.Capability{forge.CapMembership}, pastes: true, deployment: true,
		call: func(ctx context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key, a sweepArgs) (any, error) {
			return reg.IsActiveMember(ctx, s.Host(), a.namespace, a.username)
		},
	},
	{
		// It names endpoints on the host and asks nothing: the code is
		// redeemed by whoever signs a person in, never by the driver.
		name: "OAuth", caps: []forge.Capability{forge.CapSignIn}, offline: true,
		call: func(_ context.Context, reg *forge.Registry, s *forge.Session, _ repokey.Key, _ sweepArgs) (any, error) {
			return reg.OAuth(s.Host())
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

// hop is one request this driver made, as the recorder UNDER the credential pin
// saw it — so the header here is the one the pin attached, on the hop it
// attached it to, and a request replayed at a second address is a second hop
// whatever address that was.
type hop struct {
	shape string
	// query is the encoded query this request carried. It is recorded apart
	// from the shape — a page counter is not a different endpoint — and pinned
	// on its own below, because it is still part of what was asked.
	query      string
	credential string
	// carried is whether the header was THERE, which is not the same question as
	// whether it had anything in it: "Authorization: Bearer " is a header, and it
	// turns a public read into a 401.
	carried bool
	body    string
}

// wire records every hop, in order, and answers through the transport beneath
// it. A request the pin refuses never reaches it, which is the point: that is a
// request this driver asked to have made.
type wire struct {
	base http.RoundTripper

	mu   sync.Mutex
	hops []hop
}

func (w *wire) RoundTrip(r *http.Request) (*http.Response, error) {
	w.mu.Lock()
	_, carried := r.Header["Authorization"]
	w.hops = append(w.hops, hop{
		shape:      shapeOf(r),
		query:      r.URL.RawQuery,
		credential: r.Header.Get("Authorization"),
		carried:    carried,
		body:       bodyOf(r),
	})
	w.mu.Unlock()
	return w.base.RoundTrip(r)
}

func (w *wire) sent() []hop {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]hop(nil), w.hops...)
}

func (w *wire) shapes() []string {
	out := []string{}
	for _, h := range w.sent() {
		out = append(out, h.shape)
	}
	return out
}

// queries is every request that carried one, as "shape?query".
func (w *wire) queries() []string {
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
func (w *wire) bodies() []string {
	out := []string{}
	for _, h := range w.sent() {
		if h.body == "" {
			continue
		}
		out = append(out, h.shape+"\n\t"+h.body)
	}
	return out
}

func (w *wire) reset() {
	w.mu.Lock()
	w.hops = nil
	w.mu.Unlock()
}

// shapeOf is a request shape: the method and the escaped path this driver
// composed. The query is deliberately absent — a page counter is not a
// different endpoint — and the path is the ESCAPED one, because whether a
// consumer's string stayed inside its segment is exactly what the escaping
// sweep reads here.
func shapeOf(r *http.Request) string { return r.Method + " " + r.URL.EscapedPath() }

// bodyOf is what a request carried, read through GetBody so that reading it
// does not consume the one the driver is about to send. It is re-marshalled
// from a decode, so what the golden list below pins is the FIELDS AND VALUES
// and not the order a struct happens to render them in.
func bodyOf(r *http.Request) string {
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

// maxSweepBody is larger than any body this driver composes and small enough
// that a bug producing an unbounded one is a failure rather than a hang.
const maxSweepBody = 1 << 20

// ─────────────────────────────── the forge ───────────────────────────────

// sweepForge is a GitHub that answers everything correctly, and answers ONE
// request shape with a 3xx when it is armed to.
type sweepForge struct {
	srv       *httptest.Server
	elsewhere *httptest.Server

	// owner and name are the repository every answer describes, whatever path
	// the request arrived on. See the file comment: the successor names the
	// victim.
	owner, name string

	mu       sync.Mutex
	armed    string
	status   int
	offHost  bool
	fired    bool
	arrived  []string
	elsewhen []string
}

type sweepSubject struct {
	forge *sweepForge
	wire  *wire
	drv   forge.Driver
	reg   *forge.Registry
	sess  *forge.Session
	key   repokey.Key
}

func newSweepForge(t *testing.T, op sweepOp) *sweepSubject {
	t.Helper()
	return newSweepForgeNaming(t, op, "", "")
}

// newSweepForgeNaming is the same subject with the session's key SPELLED
// differently in the clone URL it is derived from.
//
// The key is parsed rather than built, so a hostile spelling here is a key
// repokey really produces and not one a test imagined: whatever survives
// repokey's grammar is exactly the population this driver has to escape. The
// forge keeps describing the benign repository, because what a hostile key is
// about is the REQUEST this driver composes from it.
func newSweepForgeNaming(t *testing.T, op sweepOp, ownerSpelling, nameSpelling string) *sweepSubject {
	t.Helper()

	owner := op.owner
	if owner == "" {
		owner = namespace
	}
	f := &sweepForge{owner: owner, name: "pay"}
	if ownerSpelling == "" {
		ownerSpelling = owner
	}
	if nameSpelling == "" {
		nameSpelling = f.name
	}

	// The off-host destination is a loopback listener on ANOTHER port, so
	// "another host" is a real host this file started rather than a name that
	// would have to be resolved. It records what reaches it, and nothing may.
	f.elsewhere = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.elsewhen = append(f.elsewhen, shapeOf(r))
		f.mu.Unlock()
	}))
	t.Cleanup(f.elsewhere.Close)

	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	host := strings.TrimPrefix(f.srv.URL, "https://")

	w := &wire{base: f.srv.Client().Transport}
	drv, err := github.New(host, github.Transport(w), github.App(appClientID, testKeyPEM()))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://" + host + "/" + ownerSpelling + "/" + nameSpelling)
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(goodToken))
	require.NoError(t, err)

	return &sweepSubject{forge: f, wire: w, drv: drv, reg: reg, sess: sess, key: key}
}

// arm points the next arrival of one shape somewhere else. offHost chooses
// which hop the row is about: a location on this very host, which
// forge.PinnedTransport permits and MUST — a per-hop host check is the whole of
// what a pin can decide — or one on the loopback listener next door, which it
// refuses one layer too late to be the thing that saves a write.
func (s *sweepSubject) arm(shape string, status int, offHost bool) {
	s.wire.reset()
	f := s.forge
	f.mu.Lock()
	f.armed, f.status, f.offHost, f.fired = shape, status, offHost, false
	f.arrived, f.elsewhen = nil, nil
	f.mu.Unlock()
}

func (f *sweepForge) serve(w http.ResponseWriter, r *http.Request) {
	shape := shapeOf(r)

	f.mu.Lock()
	f.arrived = append(f.arrived, shape)
	move := f.armed != "" && shape == f.armed && !f.fired
	if move {
		// ONCE. A location that is the same shape again — which is what a path
		// with no namespace in it gets — would otherwise be an infinite loop
		// that Go stops at ten hops, and a row that passed on THAT would be
		// saying nothing about this driver.
		f.fired = true
	}
	status, offHost := f.status, f.offHost
	f.mu.Unlock()

	if move {
		http.Redirect(w, r, f.location(r, offHost), status)
		return
	}
	f.answer(w, r)
}

// location is where this forge points. On the same host it is the same request
// under another namespace, which is what a renamed or TRANSFERRED repository
// answers and what re-registering the freed name turns into a grant; a request
// naming no namespace is pointed back at itself with a marker, so that a second
// hop is visible whatever was asked.
func (f *sweepForge) location(r *http.Request, offHost bool) string {
	uri := r.URL.RequestURI()
	if offHost {
		return f.elsewhere.URL + uri
	}
	if to := strings.Replace(uri, f.owner, "attacker", 1); to != uri {
		return to
	}
	if strings.Contains(uri, "?") {
		return uri + "&hop=1"
	}
	return uri + "?hop=1"
}

// answer is the second hop, and everything on it succeeds: a driver that
// arrives here must not be able to tell it was somewhere else.
func (f *sweepForge) answer(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && (strings.HasSuffix(path, "/keys") || strings.HasSuffix(path, "/hooks")):
		// A short page of one, so a listing stops here.
		sweepJSON(w, `[{"id":991,"title":"tau","key":"ssh-ed25519 AAAA","config":{"url":"https://build.example.invalid/hook"}}]`)
	case strings.HasSuffix(path, "/keys"), strings.HasSuffix(path, "/hooks"):
		sweepJSON(w, `{"id":991}`)
	case strings.HasSuffix(path, "/installation"):
		sweepJSON(w, `{"id":`+strconv.Itoa(installationID)+`}`)
	case strings.HasSuffix(path, "/access_tokens"):
		sweepJSON(w, `{"token":"ghs_minted_for_the_walk"}`)
	case strings.Contains(path, "/memberships/"):
		sweepJSON(w, `{"state":"active"}`)
	case strings.HasSuffix(path, apiPrefix+"user"):
		// WHO THE CREDENTIAL IS, and it is the fixed login of this package's
		// fixtures — so a key owned by anybody else takes Create's organization
		// endpoint and a key owned by this login takes the personal one. Both
		// are operations above; neither is named here.
		sweepJSON(w, `{"login":"`+login+`","id":`+strconv.Itoa(userID)+`}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/repos"):
		sweepJSON(w, f.repository(r, f.created(r)))
	case strings.HasSuffix(path, apiPrefix+"user/repos"):
		// A short page, so the listing stops here rather than paging forever.
		sweepJSON(w, "["+f.repository(r, f.name)+"]")
	default:
		sweepJSON(w, f.repository(r, f.name))
	}
}

// created reads back the name the create request asked for, so that the
// repository this forge reports is the one the caller named — and Create's own
// "the forge created something else" check is therefore not what fails a
// redirect row.
func (f *sweepForge) created(r *http.Request) string {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.Name == "" {
		return f.name
	}
	return body.Name
}

func (f *sweepForge) repository(r *http.Request, name string) string {
	full := f.owner + "/" + name
	return `{"full_name":"` + full + `",
		"clone_url":"https://` + r.Host + `/` + full + `.git",
		"ssh_url":"ssh://git@` + r.Host + `/` + full + `.git",
		"default_branch":"main","private":false,
		"permissions":{"admin":true,"maintain":true,"push":true,"pull":true}}`
}

func sweepJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

func (f *sweepForge) moved() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fired
}

func (f *sweepForge) reachedElsewhere() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.elsewhen...)
}

// ─────────────────────────────── discovery ───────────────────────────────

// sweepShapes runs every operation against a forge that answers everything
// correctly and reports, per operation, the ordered shapes it put on the wire.
// It is the population every property below runs over, and it is read off the
// driver rather than written down.
func sweepShapes(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, op := range sweepOps {
		s := newSweepForge(t, op)
		got, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err,
			"%s failed against a forge that answers everything correctly, so what it sends was never discovered and every property below skips it (it returned %v)",
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
// this file believes in: everything below is driven from the discovered set, so
// a new endpoint is swept whether or not it is written here — this is the line
// that makes it also VISIBLE.
var sweptShapes = []string{
	"DELETE /api/v3/repos/acme/pay",
	"DELETE /api/v3/repos/acme/pay/hooks/7",
	"DELETE /api/v3/repos/acme/pay/keys/7",
	"GET /api/v3/orgs/acme/installation",
	"GET /api/v3/orgs/acme/memberships/alice",
	"GET /api/v3/repos/acme/pay",
	"GET /api/v3/repos/acme/pay/hooks",
	"GET /api/v3/repos/acme/pay/keys",
	"GET /api/v3/user",
	"GET /api/v3/user/repos",
	"POST /api/v3/app/installations/4242/access_tokens",
	"POST /api/v3/orgs/acme/repos",
	"POST /api/v3/repos/acme/pay/hooks",
	"POST /api/v3/repos/acme/pay/keys",
	"POST /api/v3/user/repos",
}

// TestTheSweepDiscoversEveryRequestShapeThisDriverIssues.
func TestTheSweepDiscoversEveryRequestShapeThisDriverIssues(t *testing.T) {
	shapes := sweepShapes(t)
	found := sweepPopulation(shapes)
	for _, op := range sweepOps {
		t.Logf("%-38s %v", op.name, shapes[op.name])
	}
	assert.Equal(t, sweptShapes, found,
		"the requests this driver makes changed; every property in this file runs over the discovered set, so read the diff and say whether the new one is armed the way the others are")
}

// sweptBodies is every request this driver composes that CARRIES a body,
// pinned by value.
//
// A path says which resource a request is about; a body says what it asks to be
// done to it, and until this list nothing read most of one. Measured: the hook's
// name stopped being "web", the deploy key stopped carrying the title its caller
// chose, Create stopped sending Private and stopped sending Description — four
// separate mutations, four times the whole tree at exit 0. A repository created
// public when the caller asked for private is the sharpest of them, because tau
// creates configuration repositories.
//
// IT IS THE WHOLE BODY AND NOT A FIELD LIST, for the reason the gitlab driver's
// hook test gives: a field ADDED here is a value nobody decided, and only
// equality fails on one.
var sweptBodies = []string{
	"POST /api/v3/app/installations/4242/access_tokens\n\t" +
		`{"permissions":{"members":"read"}}`,
	"POST /api/v3/orgs/acme/repos\n\t" +
		`{"description":"` + sweepDescription + `","name":"pay","private":true}`,
	"POST /api/v3/repos/acme/pay/hooks\n\t" +
		`{"active":true,"config":{"content_type":"json","insecure_ssl":"0","secret":"` + sweepHookWire() + `","url":"` + sweepCallback + `"},"events":["push"],"name":"web"}`,
	"POST /api/v3/repos/acme/pay/keys\n\t" +
		`{"key":"ssh-ed25519 AAAAC3Nz","read_only":true,"title":"tau"}`,
	"POST /api/v3/user/repos\n\t" +
		`{"description":"` + sweepDescription + `","name":"pay","private":false}`,
}

// sweepHookWire is the string the core derives from sweepSecret under this
// driver's scheme. It is derived rather than written down, because a driver that mangled
// the wire string would otherwise be pinned to its own mangling.
func sweepHookWire() string {
	wire, err := forge.SchemeHubSHA256.Wire(forge.SecretFrom([]byte(sweepSecret)))
	if err != nil {
		panic(err)
	}
	return wire
}

// TestTheSweepPinsEveryRequestBodyThisDriverComposes.
func TestTheSweepPinsEveryRequestBodyThisDriverComposes(t *testing.T) {
	found := []string{}
	for _, op := range sweepOps {
		s := newSweepForge(t, op)
		_, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against a forge that answers everything correctly", op.name)
		found = append(found, s.wire.bodies()...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	for _, b := range found {
		t.Log(b)
	}
	assert.Equal(t, sweptBodies, found,
		"a request body this driver composes changed; every field in one is a decision, so read the diff and say which decision this is")
}

// sweptQueries is every request this driver composes that CARRIES a query,
// pinned by value.
//
// A path says which resource, a body says what to do to it, and a query says
// which slice of it — and until this list nothing read one. The listing's
// per_page is the only thing that makes the page counter terminate on a short
// page, and a driver that stopped sending it would page a hundred repositories
// at GitHub's default thirty and stop after the first page believing it had
// them all.
var sweptQueries = []string{
	"GET /api/v3/repos/acme/pay/hooks?page=1&per_page=100",
	"GET /api/v3/repos/acme/pay/keys?page=1&per_page=100",
	"GET /api/v3/user/repos?page=1&per_page=100",
}

// TestTheSweepPinsEveryQueryThisDriverComposes.
func TestTheSweepPinsEveryQueryThisDriverComposes(t *testing.T) {
	found := []string{}
	for _, op := range sweepOps {
		s := newSweepForge(t, op)
		_, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against a forge that answers everything correctly", op.name)
		found = append(found, s.wire.queries()...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	for _, q := range found {
		t.Log(q)
	}
	assert.Equal(t, sweptQueries, found,
		"a query this driver composes changed; every parameter in one is a decision, so read the diff and say which decision this is")
}

// TestEveryCapabilityThisDriverAnswersIsSwept is the closure on the one
// hand-written list here. A face this driver grows is a capability
// forge.Session.Capabilities computes from a type assertion, and an operation
// nobody added for it would be a question the sweep never asks.
func TestEveryCapabilityThisDriverAnswersIsSwept(t *testing.T) {
	s := newSweepForge(t, sweepOps[0])

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
// what was actually armed is collected and compared with what was discovered: a
// request this driver makes and this loop does not reach fails the property
// rather than quietly shrinking it.
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
			// The shape is in the subtest name so a failure names the request
			// and not a number. Its slashes would nest the name, so they are
			// spelled with dots.
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
// makes must stop at it rather than act on whatever the forge pointed at.
func TestNoRequestThisDriverMakesFollowsASameHostRedirect(t *testing.T) {
	sweepRows(t, func(t *testing.T, op sweepOp, index int, shape string) {
		s := newSweepForge(t, op)
		for _, status := range redirectStatuses {
			t.Run(strconv.Itoa(status), func(t *testing.T) {
				s.arm(shape, status, false)
				got, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
				sent := s.wire.sent()
				t.Logf("%s -> %v, err = %v", op.name, got, err)
				t.Logf("the driver made %v", sent)

				require.Truef(t, s.forge.moved(),
					"%q never arrived, so this row moved nothing and says nothing", shape)
				require.Errorf(t, err,
					"%s followed a %d on %q and answered as if the forge had spoken about %q",
					op.name, status, shape, s.key)
				assert.Contains(t, err.Error(), "answered "+strconv.Itoa(status),
					"the error must name the 3xx: an operator whose repository was renamed has to be able to tell that from an outage")
				require.Lenf(t, sent, index+1,
					"the request was replayed at the location the forge chose, carrying this credential: %v", sent)
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
// would have refused this hop, but only after being asked to make it, and by
// then a 307 has already replayed the body once for every host that answers.
func TestNoRequestThisDriverMakesFollowsARedirectOffTheHost(t *testing.T) {
	sweepRows(t, func(t *testing.T, op sweepOp, index int, shape string) {
		s := newSweepForge(t, op)
		for _, status := range redirectStatuses {
			t.Run(strconv.Itoa(status), func(t *testing.T) {
				s.arm(shape, status, true)
				got, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
				sent := s.wire.sent()
				t.Logf("%s -> %v, err = %v", op.name, got, err)

				require.Truef(t, s.forge.moved(), "%q never arrived, so this row moved nothing", shape)
				require.Errorf(t, err, "%s followed a %d off the host on %q", op.name, status, shape)
				assert.Contains(t, err.Error(), "answered "+strconv.Itoa(status))
				assert.Empty(t, s.forge.reachedElsewhere(),
					"a request this driver built reached another host")
				require.Lenf(t, sent, index+1, "the request was replayed off the host: %v", sent)

				var leak *forge.CredentialLeakError
				assert.False(t, errors.As(err, &leak),
					"the driver asked the pin to make the second request instead of refusing to make one")
				assert.NotContains(t, err.Error(), goodToken)
			})
		}
	})
}

// TestAnAnonymousSessionPutsNoCredentialHeaderOnAnyRequest.
//
// An anonymous session is a real state — GitHub answers public repositories to
// nobody in particular — and the driver's own line for it is `if token != ""`
// around the header. Deleting that guard left the whole tree at exit 0, because
// the assertion an anonymous test can most easily make reads the header's VALUE:
// "Authorization: Bearer " is empty by that reading and present by GitHub's, and
// GitHub answers a present-but-meaningless credential with a 401. The public
// read then fails as if the repository were gone.
//
// So this reads presence, and it reads it on every request the sweep found
// rather than on one. Whichever operations an anonymous session is allowed to
// make, none of their hops may carry the header at all.
func TestAnAnonymousSessionPutsNoCredentialHeaderOnAnyRequest(t *testing.T) {
	reached := 0
	for _, op := range sweepOps {
		t.Run(op.name, func(t *testing.T) {
			s := newSweepForge(t, op)
			anon, err := s.reg.For(s.key, forge.Anonymous())
			require.NoError(t, err)

			got, err := op.call(ctx(), s.reg, anon, s.key, benignArgs)
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
					"an anonymous session put an Authorization header on %q; the forge reads that as a credential it was given and refuses the read", h.shape)
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
// five below are the whole population.
//
// WHAT COUNTS AS ONE IS WHERE THE STRING GOES, NOT WHERE IT CAME FROM, and that
// distinction is the defect this replaced. The previous mechanism swept three
// slots — an opaque handle and the two halves of a membership question — on the
// stated ground that they are "strings a consumer typed", as opposed to a key
// "repokey vouched for and Addressable checked". Neither vouching is about a
// path. repokey splits on "/", refuses whitespace, and refuses a segment that
// IS "." or ".."; it says nothing about "?", "#" or "%", and it decodes what a
// clone URL escaped — so https://h/acme/pay%23evil is a key whose Name is
// "pay#evil". Addressable refuses an owner with a "/" in it and says nothing
// about the name at all. The two halves of a key are therefore exactly as
// consumer-supplied as a handle is — a consumer types the clone URL — and they
// were left out by provenance rather than by destination.
//
// Measured before this: with the key left out, five of this driver's seven
// url.PathEscape calls could be deleted one at a time with the WHOLE forge tree
// at exit 0 — repoPath's two, Create's organization path, and both halves of
// the clone address.
//
// AND A CLONE ADDRESS IS IN THE POPULATION. It is not a request, so the sweep's
// request rows never reach it, and it is a URL this driver composes out of a
// key and hands to whoever clones with it. The property below therefore runs
// over every path an operation COMPOSED, which is the requests it made plus the
// addresses it returned.
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
// unescaped one TRUNCATES the request; and "%2f" is a "/" the forge decodes
// after this driver has stopped looking, which is what makes ".." in front of
// it a walk into another repository rather than a name with dots in it.
//
// It has no literal "/" because repokey splits on one, and a test that fed a
// key an impossible spelling would be arming a population that does not exist.
const walkingSegment = "..%2f..%2fattacker?a#b"

// walkingSegmentSpelt is how a clone URL writes that segment. The two are
// asserted to correspond in TestTheHostileKeyIsOneRepokeyReallyProduces rather
// than assumed, because everything below is only about a real population if
// repokey really produces this one.
const walkingSegmentSpelt = "..%252f..%252fattacker%3Fa%23b"

// keySweepSlots are the two halves of the KEY, which reach this driver through
// repokey rather than from a consumer directly.
func keySweepSlots() []sweepSlot {
	return []sweepSlot{
		{
			name: "the repository's owner", args: benignArgs,
			ownerSpelling: walkingSegmentSpelt, value: walkingSegment,
		},
		{
			name: "the repository's name", args: benignArgs,
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

	drv, err := github.New("h.example")
	require.NoError(t, err)
	assert.NoError(t, drv.Addressable(k),
		"this driver refuses the key the escaping sweep is written against, so the sweep would prove nothing")
}

// composedURL is one URL this driver put a string into: a request it made, or
// an address it handed back. Both halves are here because a string can leave
// its place in either of them — a "?" that survived unescaped in a path becomes
// a query parameter, and a consumer string pasted into a query value can become
// a second parameter.
type composedURL struct {
	// what is the method, or ADDRESS for a URL that was returned rather than
	// sent.
	what  string
	path  string // escaped
	query string // encoded
	// route is the route a request matched, and nil for an address. What a
	// route declares is what its query may name.
	route *github.RouteFact
}

func (c composedURL) String() string {
	if c.query == "" {
		return c.what + " " + c.path
	}
	return c.what + " " + c.path + "?" + c.query
}

// composed is every URL one operation put a string into, with each request
// bound to the route that produced it.
func composed(t *testing.T, hops []hop, drv forge.Driver, got any) []composedURL {
	t.Helper()
	out := []composedURL{}
	for _, h := range hops {
		method, path, found := strings.Cut(h.shape, " ")
		require.Truef(t, found, "%q is not a request shape", h.shape)
		matched := assertOneRoute(t, drv, h.shape)
		var r *github.RouteFact
		if len(matched) == 1 {
			r = &matched[0]
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

// decodedSegments is a composed path split into the segments a server will see,
// each one decoded exactly once.
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
// decoded once, which is what the forge on the other end does, and the hostile
// value must come back out of exactly that. Escaping it correctly is the only
// way that happens: pasted raw, "..%2f..%2fattacker?a#b" arrives as the segment
// "../../attacker" with a query and a fragment the driver never wrote — and
// note that it arrives with the SAME NUMBER OF SEGMENTS, which is why counting
// separators is not enough and was not enough.
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
					b := newSweepForge(t, op)
					benignGot, benignErr := op.call(ctx(), b.reg, b.sess, b.key, benignArgs)
					t.Logf("benign  %s -> %v, err = %v", op.name, benignGot, benignErr)
					benign := composed(t, b.wire.sent(), b.drv, benignGot)

					s := newSweepForgeNaming(t, op, slot.ownerSpelling, slot.nameSpelling)
					got, err := op.call(ctx(), s.reg, s.sess, s.key, slot.args)
					t.Logf("hostile %s -> %v, err = %v", op.name, got, err)
					urls := composed(t, s.wire.sent(), s.drv, got)
					t.Logf("the driver composed %v", urls)

					// FEWER IS FINE AND MORE IS NOT. A hostile value can make
					// this driver stop earlier than a benign one — Create's own
					// "the forge resolved something else" refusal does exactly
					// that — and that is a refusal rather than a defect. An
					// EXTRA URL is a request a consumer's string caused.
					require.LessOrEqualf(t, len(urls), len(benign),
						"%s composed more URLs for a hostile %s than for a benign one", op.name, slot.name)

					changed := []composedURL{}
					for i, u := range urls {
						// A REQUEST STILL NAMES ONLY WHAT ITS ROUTE DECLARES.
						// That is "the string opened nothing of its own" said
						// against the driver's own routing rather than against a
						// benign twin — and the twin is the wrong comparison,
						// because a hostile owner legitimately flips Create from
						// the personal endpoint to the organization one, which
						// has a segment more.
						if u.what == "ADDRESS" {
							assert.Emptyf(t, u.query, "a clone address carries a query: %v", u)
						} else {
							require.NotNilf(t, u.route, "%v matched no route", u)
							for name := range parsedQuery(t, u.query) {
								assert.Containsf(t, u.route.Params, name,
									"%v carries a query parameter %s %s does not declare, so a string walked out of where it was put",
									u, u.route.Method, u.route.Template)
							}
						}

						// A URL THE HOSTILE STRING DID NOT CHANGE IS A URL IT
						// IS NOT IN. Create's first request reads who the
						// credential is and names no repository; the mint in the
						// middle of the membership walk names only an id the
						// forge chose. Those are not this row's business, and
						// pairing by position is what tells them from a URL the
						// string reached.
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
//	DELETE /api/v3/repos/acme/site/keys/..    path.Clean -> /api/v3/repos/acme/site
//	DELETE /api/v3/repos/acme/site/keys/.     path.Clean -> /api/v3/repos/acme/site/keys
//
// The first is GitHub's delete-a-repository endpoint and the second is the
// collection. Go's own httptest server answered the first with a 307 to the
// cleaned path, so this is not a claim about hypothetical proxies: it is what
// the wire carried.
//
// THE ASSERTION IS ABOUT EVERY URL AND NOT ONLY THE REFUSED ONE. IsActiveMember
// with a hostile username still makes the two requests that come before the one
// it refuses, so "no request at all" would be false and would have to be
// weakened per-operation. What holds everywhere is that NOTHING THIS DRIVER
// COMPOSED CAN BE MOVED BY A RESOLVER — a fixed point under RFC 3986 dot-segment
// removal, in the escaped spelling an intermediary resolves and in the decoded
// spelling a server holds. The refusal is then asserted where the string really
// arrives, so no row passes by the string never having been read.
func TestNoStringThisDriverPastesIntoAURLCanMoveThePathItIsIn(t *testing.T) {
	for _, slot := range consumerSlots {
		t.Run(slot.name, func(t *testing.T) {
			for name, spelling := range spellingsFor(slot) {
				t.Run(name, func(t *testing.T) {
					refused := []string{}
					for _, op := range sweepOps {
						s := newSweepForge(t, op)
						got, err := op.call(ctx(), s.reg, s.sess, s.key, slot.args(spelling))
						t.Logf("%s -> %v, err = %v", op.name, got, err)

						for _, u := range composed(t, s.wire.sent(), s.drv, got) {
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
// THE DECODED SPELLING IS THE ONE ASKED ABOUT, because it is what a server holds
// after the one decode it performs — and because it SUBSUMES the escaped one. An
// escaped component is a dot segment only when its decoded form is, and the
// decoded spelling additionally catches "../..", which escapes to "..%2F..": one
// innocent segment on the wire and two dot components to anything that decodes
// before it routes. Asserting the escaped path beside it would be asserting this
// twice, which is how a line comes to be deletable at exit 0.
func assertStaysPut(t *testing.T, op string, u composedURL) {
	t.Helper()
	decoded := strings.Join(decodedSegments(t, u.path), "/")
	assert.Equalf(t, decoded, path.Clean(decoded),
		"%s composed %v, and removing dot segments from %q moves it to %q: the request addresses something the caller never named",
		op, u, decoded, path.Clean(decoded))
}
