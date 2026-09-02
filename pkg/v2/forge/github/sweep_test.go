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
	"slices"
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

// walkingSpelling is ONE string standing in for every consumer-supplied string,
// and it carries all three characters a URL path gives meaning to. "/" opens a
// segment, so an unescaped one addresses a resource under another repository —
// RemoveDeployKey(acme/pay, "../../attacker/pay/keys/1") deletes another
// repository's deploy key with the caller's credential. "?" ends the path, so an
// unescaped one turns the rest into query parameters this driver never wrote.
// "#" ends the URL, so an unescaped one silently TRUNCATES the request: removing
// the handle "1#x" removes the resource numbered 1.
//
// Because it is one string, one needle decides all three: escaped exactly once,
// it appears on the wire as url.PathEscape spells it, and it appears that way
// only if every one of the three survived as itself.
const walkingSpelling = "../../attacker?a#b"

var (
	benignArgs = sweepArgs{handle: "7", namespace: namespace, username: member}

	walkingArgs = sweepArgs{
		handle:    walkingSpelling,
		namespace: walkingSpelling,
		username:  walkingSpelling,
	}
)

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
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true})
			return r.Key, err
		},
	},
	{
		// AND THE OTHER BRANCH. POST /orgs/{me}/repos is a 404 because a user is
		// not an organization, so a key naming the credential's own account
		// takes a different endpoint — the one that was armed by nothing for
		// five rounds, because the only probe that reached Create answered
		// /api/v3/user with a login the key's owner never was.
		name: "Create under the caller's own account", caps: []forge.Capability{forge.CapCreate},
		owner: login,
		call: func(ctx context.Context, _ *forge.Registry, s *forge.Session, k repokey.Key, _ sweepArgs) (any, error) {
			r, err := s.Create(ctx, k, forge.CreateOptions{Private: true})
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
		name: "IsActiveMember", caps: []forge.Capability{forge.CapMembership}, pastes: true,
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

// hop is one request this driver made, as the recorder UNDER the credential pin
// saw it — so the header here is the one the pin attached, on the hop it
// attached it to, and a request replayed at a second address is a second hop
// whatever address that was.
type hop struct {
	shape      string
	credential string
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
	w.hops = append(w.hops, hop{shape: shapeOf(r), credential: r.Header.Get("Authorization")})
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
	reg   *forge.Registry
	sess  *forge.Session
	key   repokey.Key
}

func newSweepForge(t *testing.T, op sweepOp) *sweepSubject {
	t.Helper()

	owner := op.owner
	if owner == "" {
		owner = namespace
	}
	f := &sweepForge{owner: owner, name: "pay"}

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
	key, err := repokey.Parse("https://" + host + "/" + owner + "/" + f.name)
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(goodToken))
	require.NoError(t, err)

	return &sweepSubject{forge: f, wire: w, reg: reg, sess: sess, key: key}
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

// TestEveryConsumerStringStaysInsideOnePathSegment is ONE MECHANISM for the
// whole population, rather than a test per string somebody remembered.
//
// A forge.Handle is opaque — nothing in this package parses or orders one — and
// the two halves of a membership question are strings a consumer typed. None of
// the three is a key repokey vouched for or a shape Addressable checked, and all
// three are pasted into API paths. Measured before this test: deleting
// url.PathEscape from handleSegment left the WHOLE forge tree at exit 0, and
// RemoveDeployKey(acme/pay, "../../attacker/pay/keys/1") sent
// DELETE /api/v3/repos/acme/pay/keys/../../attacker/pay/keys/1 — another
// repository's deploy key removed with the caller's credential.
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
			s := newSweepForge(t, op)
			got, err := op.call(ctx(), s.reg, s.sess, s.key, walkingArgs)
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
