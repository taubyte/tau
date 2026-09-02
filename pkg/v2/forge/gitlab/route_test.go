package gitlab

// THE ROUTING TABLE IS WHAT THE SWEEP'S EXPECTATION COMES FROM.
//
// The sweep next door discovers what this driver sends by sending it: every
// operation is run once and what arrived is read off the recorder. That finds
// every ENDPOINT some operation reaches, and it finds nothing about a BRANCH no
// input reaches — and a branch is a request just as much as an endpoint is.
// Measured on the github driver in this tree, which had the same shape: adding
//
//	if strings.HasPrefix(key.Name, "z") { path = "user/repos?legacy=1" }
//
// to an existing operation left the whole forge tree at exit 0.
//
// So a path is no longer a URL a call site builds. It is a route declared by
// newRoute, which registers it by constructing it, and do refuses to issue
// anything else — so the set of requests this driver CAN issue is a fact of the
// program. The rows below are the closure over that fact.

import (
	"context"
	"fmt"
	"go/ast"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// assertOneRoute is the other half of the table: a request that matches no
// route is a path composed outside it, and a request that matches two is a
// table in which "this route was reached" can be satisfied by another route's
// traffic.
func assertOneRoute(t *testing.T, cfg *config, shape string) []*route {
	t.Helper()
	return routesMatching(t, cfg, declaredRoutes(), shape)
}

// routesMatching asks that of ONE TABLE rather than of the package's own. The
// closures below walk a table with a route inserted into it, so they cannot ask
// through a function that goes and fetches the real one — the inserted route
// would not be in the answer, and "was it reached" would be decided for it by
// different code than for every other route.
func routesMatching(t *testing.T, cfg *config, table []*route, shape string) []*route {
	t.Helper()
	method, path, found := strings.Cut(shape, " ")
	require.Truef(t, found, "%q is not a request shape", shape)

	matched := []*route{}
	for _, r := range table {
		if r.matches(cfg.base, method, path) {
			matched = append(matched, r)
		}
	}
	assert.Lenf(t, matched, 1, "%q matched %d routes; every request this driver makes is one route's", shape, len(matched))
	return matched
}

// declaredRoutes is the table as it stands, copied out from under the lock.
func declaredRoutes() []*route {
	routesMu.Lock()
	defer routesMu.Unlock()
	return append([]*route(nil), routeTable...)
}

// ─────────────────── the closure, and its one invocation path ───────────────────

// A CLOSURE THAT IS A LOOP WITH AN ASSERT IN IT GUARDS NOTHING BUT ITSELF.
// Round 7 measured the shape next door: the ledger's rules were proven against
// a recorder and then APPLIED with a discarded one, and the whole suite passed
// at exit 0 with a real contract assertion deleted. The two rows here had the
// same shape. Measured on this driver: declare
//
//	routeMutant = newRoute(http.MethodGet, "api/v4/projects/"+hole+"/mutant")
//
// and the row below fails naming route.go:120, which is the closure working;
// then change one token in the row — `range table` to `range table[:0]` — and
// the whole package is back at exit 0 with that request still declared and
// still armed by nothing. Nothing else in the package says a word about it,
// because nothing else applies the table to anything.
//
// SO THERE IS ONE CALL SITE AND IT PROVES BOTH HALVES AT ONCE. The closure is
// the pure function below; the row inserts a route NOTHING REACHES into the
// table, walks the table with it, and requires the complaints to be EXACTLY
// that route's:
//
//   - an empty answer means the closure did not run — switched off, or ranged
//     over a subset that skipped the position the inserted route is at, which
//     is why it is inserted at every position in turn;
//   - an extra line is a route this driver really ships that the sweep really
//     did not reach, named by the line that declared it. That is the original
//     property, unweakened: the inserted route is one more row in the answer,
//     never a substitute for a row.
//
// AND THE OBSERVATION IS BUILT AFTER THE INSERTION, from the same matcher, so
// "reached" is decided for the inserted route by the code that decides it for
// every other one. Marking the table's routes reached to quiet the row marks
// this one too, and the complaint it must make stops being made.
//
// WHERE THE TOWER ENDS, and it ends here rather than at another rung. What must
// be correct is: sweepShapes returns what the driver really put on the wire;
// sweptRoutes decides "reached" by r.matches and nothing else; the row below
// runs the insertion over every position and compares for equality. That is a
// handful of lines in one place, all of them named in this comment, and it is
// the same exposure as trusting that this file is compiled into the package at
// all. A further rung would be a closure over the closure with its own
// invocation to guard, and that regress does not terminate.

// describe is how a route is named in a complaint: what it is, and the line
// that introduced it.
func describe(r *route) string {
	return fmt.Sprintf("%s %s, declared at %s", r.method, r.template, r.declared)
}

// decoyDeclared is where the route nothing reaches says it comes from. It is
// prose rather than a file:line, because a reader who sees it in a failure has
// to be able to tell it from a route this driver really declared.
const decoyDeclared = "route_test.go, as the route nothing reaches"

// decoyUnsent is a query parameter no route declares and nothing sends.
const decoyUnsent = "nothing-sends-this"

// aRouteNothingReaches is the request this driver could grow tomorrow: declared
// and SHIPPING — a route a test built to drive the plumbing is skipped by both
// closures, so a decoy that was not shipping would prove nothing about either.
//
// It carries two query parameters on purpose. "page" is one the sweep really
// sends, so a `sent` map that had been filled from the table rather than from
// the wire still answers this route correctly; decoyUnsent is one nothing
// sends, and it is the whole of what the parameter row must complain about. An
// observation that says everything was sent stops complaining about it, and an
// observation that says nothing was starts complaining about "page" as well.
func aRouteNothingReaches() *route {
	const template = "api/v4/projects/" + hole + "/nothing-reaches-this"
	return &route{
		method:   http.MethodGet,
		template: template,
		segments: strings.Split(template, "/"),
		params:   []string{"page", decoyUnsent},
		declared: decoyDeclared,
		ships:    true,
	}
}

// withRouteAt is the table with one more route in it, at the given position.
func withRouteAt(table []*route, i int, r *route) []*route {
	return slices.Insert(slices.Clone(table), i, r)
}

// unreachedRoutes IS THE CLOSURE over the routing table: every route this
// driver ships that the sweep never reached, as one complaint each.
func unreachedRoutes(table []*route, reached map[*route]bool) []string {
	out := []string{}
	for _, r := range table {
		// A route a test built to drive the plumbing is not a request this
		// driver can issue.
		if !r.ships || reached[r] {
			continue
		}
		out = append(out, describe(r))
	}
	return out
}

// sweptRoutes is the observation the closure is applied to: which routes OF
// THIS TABLE the sweep's request shapes reached.
func sweptRoutes(t *testing.T, cfg *config, table []*route, shapes map[string][]string) map[*route]bool {
	t.Helper()
	reached := map[*route]bool{}
	for _, op := range sweepOps {
		for _, shape := range shapes[op.name] {
			for _, r := range routesMatching(t, cfg, table, shape) {
				reached[r] = true
			}
		}
	}
	return reached
}

// parametersNeverSent is the same closure one level down: every query parameter
// a shipping route may carry that the sweep never saw one of.
func parametersNeverSent(table []*route, sent map[string]bool) []string {
	out := []string{}
	for _, r := range table {
		if !r.ships {
			continue
		}
		for _, param := range r.params {
			if sent[param] {
				continue
			}
			out = append(out, fmt.Sprintf("%s, may carry the query parameter %q", describe(r), param))
		}
	}
	return out
}

// TestEveryRouteThisDriverCanIssueIsSwept.
//
// This is the row a new branch fails. The sweep drives every operation; a route
// that no operation reached is a request this driver can issue that nothing in
// the sweep arms — not the redirect rows, not the anonymous row, not the
// escaping rows — and it is named here by the line that declared it rather than
// discovered a round later.
//
// WHAT THIS CLOSES AND WHAT IT DOES NOT, measured rather than reasoned. A
// branch that reaches a NEW route fails here; a branch that adds a query
// PARAMETER fails the row below; a branch that adds one without declaring it is
// refused by the driver. What survives is a branch that reuses a route the
// sweep already reached and changes only a VALUE it sends — measured, twice, at
// exit 0: Create sending Private false when the key's name starts with "z", and
// Create taking its own personal-account branch on the same condition. The
// first is a request the sweep never sees and the second is a request it sees
// under another name; neither is a new SHAPE, and shape is the unit here. What
// would close them is a driver DRIVEN over its own branches rather than over a
// table of operations, which is a different mechanism from this one and not a
// stronger version of it.
func TestEveryRouteThisDriverCanIssueIsSwept(t *testing.T) {
	shapes := sweepShapes(t)
	subject := newSweepInstance(t)

	table := declaredRoutes()
	require.NotEmpty(t, table, "this driver declared no routes, so nothing here is a closure over anything")

	decoy := aRouteNothingReaches()
	want := []string{describe(decoy)}
	for i := 0; i <= len(table); i++ {
		with := withRouteAt(table, i, decoy)
		assert.Equalf(t, want, unreachedRoutes(with, sweptRoutes(t, subject.cfg, with, shapes)),
			"with a route nothing reaches at position %d of %d, this closure must name it and nothing else. An extra line is a request this driver can issue that the sweep never reached — no redirect row, no anonymous row and no escaping row says anything about it. An empty answer is the closure not running over that position at all, which would arm a route there with nothing",
			i, len(table))
	}
}

// TestEveryQueryParameterThisDriverDeclaresIsSwept is the same closure one
// level down. A branch that adds a parameter to a request the sweep already
// reaches changes what is asked without changing any path — so the route
// declares which parameters it may carry, do refuses one it did not declare,
// and a declared parameter nothing sent is a parameter armed by nothing.
//
// It is applied the same way, for the reason the comment above gives: a loop
// with an assert in it is switched off by one token and says nothing about it.
func TestEveryQueryParameterThisDriverDeclaresIsSwept(t *testing.T) {
	sent := map[string]bool{}
	for _, op := range sweepOps {
		s := newSweepInstance(t)
		_, err := op.call(context.Background(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against an instance that answers everything correctly", op.name)
		for _, h := range s.wire.sent() {
			for name := range parsedQuery(t, h.query) {
				sent[name] = true
			}
		}
	}

	table := declaredRoutes()
	require.NotEmpty(t, table, "this driver declared no routes, so nothing here is a closure over anything")

	decoy := aRouteNothingReaches()
	want := []string{fmt.Sprintf("%s, may carry the query parameter %q", describe(decoy), decoyUnsent)}
	for i := 0; i <= len(table); i++ {
		with := withRouteAt(table, i, decoy)
		assert.Equalf(t, want, parametersNeverSent(with, sent),
			"with a route declaring one parameter the sweep sends and one it never does, at position %d of %d, this closure must name the second and nothing else. An extra line is a parameter this driver may carry that nothing armed; an empty answer is the closure not running over that position",
			i, len(table))
	}
}

// TestThisDriverBuildsAnHTTPRequestInExactlyOnePlace is what makes the table
// exhaustive rather than merely tidy. Every closure above is about routes, and
// a route only constrains a request that was built through do — so a second
// place that builds one is a request outside the table, armed by nothing and
// escaped by nobody.
//
// It reads the package's own source rather than trusting a comment, because
// "do is the only place" is exactly the kind of sentence that stays in a file
// after it has stopped being true.
func TestThisDriverBuildsAnHTTPRequestInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/v2/forge/gitlab")
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	pkg := loaded[0]
	require.Emptyf(t, pkg.Errors, "type-checking this driver's own source: %v", pkg.Errors)

	// RESOLVED, NOT SPELLED. The name of the import and the name of the local
	// variable are both anybody's to choose; what a call means is not.
	builders := []string{}
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			fn, ok := pkg.TypesInfo.Uses[sel.Sel].(interface {
				Name() string
				FullName() string
			})
			if !ok {
				return true
			}
			switch fn.FullName() {
			case "net/http.NewRequest", "net/http.NewRequestWithContext",
				"(*net/http.Client).Do", "(*net/http.Client).Get",
				"(*net/http.Client).Post", "(*net/http.Client).Head":
				builders = append(builders, pkg.Fset.Position(call.Pos()).String()+" "+fn.FullName())
			}
			return true
		})
	}

	// AND ONE PLACE BUILDS THE CLIENT. The pin and the redirect policy are
	// installed on an http.Client, so a second one built anywhere is a second
	// set of both — which is exactly the shape of the defect an earlier round
	// found here, where a redirect policy lived on one of this driver's two
	// clients and not the other.
	clients := []string{}
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if named, ok := pkg.TypesInfo.TypeOf(lit).(interface{ String() string }); ok && named.String() == "net/http.Client" {
				clients = append(clients, pkg.Fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	t.Logf("this driver builds an http.Client at %v", clients)
	assert.Len(t, clients, 1,
		"this driver builds more than one http.Client, so the credential pin and the redirect policy are installed in more than one place")

	t.Logf("this driver reaches the network at %v", builders)
	assert.Len(t, builders, 2,
		"this driver builds or issues an HTTP request somewhere other than do, so the routing table does not cover every request it makes")
	for _, where := range builders {
		assert.Contains(t, where, "http.go",
			"the one place a request is built is do, in http.go, and this is not it: %s", where)
	}
}

// TestARouteMatchesOnlyWhatWentThroughIt. The route table's closures rest on
// matching what went out against what was declared, so the two ways that match
// can be wrong are worth a row: a path that is not under this instance's root
// belongs to no route, and a request built outside the table is refused rather
// than issued.
func TestARouteMatchesOnlyWhatWentThroughIt(t *testing.T) {
	base, err := url.Parse("https://gitlab.example")
	require.NoError(t, err)

	assert.False(t, routeCurrentUser.matches(base, http.MethodGet, "/somewhere/else/entirely"),
		"a path outside the instance root matched a route")
	assert.False(t, routeCurrentUser.matches(base, http.MethodGet, "api/v4/user"),
		"a path that is not rooted at the instance root matched a route")
	assert.False(t, routeCurrentUser.matches(base, http.MethodPost, "/api/v4/user"),
		"a route matched another method")
	assert.True(t, routeCurrentUser.matches(base, http.MethodGet, "/api/v4/user"))

	// A request with no route at all names nothing, which is an error and not a
	// path of empty segments.
	_, err = request{}.url(base)
	require.Error(t, err)
}

// TestACloneAddressOfAKeyWithNoOwner. Addressable refuses such a key, so no
// session reaches CloneAddress with one — but the method is reachable from a
// bound client directly, and a path built from an empty owner would carry an
// empty segment.
func TestACloneAddressOfAKeyWithNoOwner(t *testing.T) {
	d, err := New("gitlab.example")
	require.NoError(t, err)
	bound, err := d.Bind(forge.Token("tok"))
	require.NoError(t, err)

	addr, err := bound.(forge.CloneAddresses).CloneAddress(repokey.Key{Host: "gitlab.example", Name: "pay"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.example/pay.git", addr.URL)
}

// TestARouteIsEscapedByTheOneFunctionThatEscapesSegments. endpoint() used to be
// where a path was composed and CloneAddress was not going through it, which is
// how a clone address kept a "#" in a project name. Both go through
// escapedSegments now, and this is the row that says so about the parts that
// have no request behind them.
func TestARouteIsEscapedByTheOneFunctionThatEscapesSegments(t *testing.T) {
	base, err := url.Parse("https://gitlab.example")
	require.NoError(t, err)
	cfg := &config{host: "gitlab.example", base: base}

	u, err := routeProject.at("acme/team/widget").url(cfg.base)
	require.NoError(t, err)
	assert.Equal(t, "/api/v4/projects/acme%2Fteam%2Fwidget", u.EscapedPath(),
		"a project is addressed by its full path URL-encoded, so the separators inside the hole are data")

	// The literals are escaped too, which is what makes "did somebody remember
	// to escape this one" not a question here.
	assert.Equal(t, "/api/v4/projects/pay%23evil", func() string {
		u, err := routeProject.at("pay#evil").url(cfg.base)
		require.NoError(t, err)
		return u.EscapedPath()
	}())
}
