package github_test

// THE ROUTING TABLE IS WHAT THE SWEEP'S EXPECTATION COMES FROM.
//
// The sweep next door discovers what this driver sends by sending it: every
// operation is run once and what arrived is read off the recorder. That finds
// every ENDPOINT some operation reaches, and it finds nothing about a BRANCH no
// input reaches — which is not hypothetical, because the endpoint Create takes
// when the key names the credential's own account was armed by nothing for five
// rounds for exactly that reason, and closing that instance left the class open:
// adding
//
//	if strings.HasPrefix(key.Name, "z") { path = "user/repos?legacy=1" }
//
// to an existing operation left the whole forge tree at exit 0.
//
// So a path is no longer a string a call site composes. It is a route declared
// by newRoute, which registers it by constructing it, and send refuses to issue
// anything else — so the set of requests this driver CAN issue is a fact of the
// program. The rows below are the closure over that fact: a route nothing
// reached is a request nothing arms, and it fails here naming the line that
// declared it.

import (
	"fmt"
	"go/ast"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/github"
)

// assertOneRoute is the other half of the table: a request that matches no
// route is a path composed outside it, and a request that matches two is a
// table in which "this route was reached" can be satisfied by another route's
// traffic.
func assertOneRoute(t *testing.T, drv forge.Driver, shape string) []github.RouteFact {
	t.Helper()
	return routesMatching(t, github.DeclaredRoutes(drv), shape)
}

// routesMatching asks that of ONE TABLE rather than of the driver's own. The
// closures below walk a table with a route inserted into it, so they cannot ask
// through a function that goes and fetches the real one — the inserted route
// would not be in the answer, and "was it reached" would be decided for it by
// different code than for every other route.
func routesMatching(t *testing.T, table []github.RouteFact, shape string) []github.RouteFact {
	t.Helper()
	method, path, found := strings.Cut(shape, " ")
	require.Truef(t, found, "%q is not a request shape", shape)

	matched := []github.RouteFact{}
	for _, r := range table {
		if r.Matches(method, path) {
			matched = append(matched, r)
		}
	}
	assert.Lenf(t, matched, 1, "%q matched %d routes; every request this driver makes is one route's", shape, len(matched))
	return matched
}

// ─────────────────── the closure, and its one invocation path ───────────────────

// A CLOSURE THAT IS A LOOP WITH AN ASSERT IN IT GUARDS NOTHING BUT ITSELF.
// Round 7 measured the shape next door: the ledger's rules were proven against
// a recorder and then APPLIED with a discarded one, and the whole suite passed
// at exit 0 with a real contract assertion deleted. The two rows here had the
// same shape. Measured on this driver: declare
//
//	routeMutant = newRoute(http.MethodGet, "repos/"+hole+"/"+hole+"/mutant")
//
// and the row below fails naming route.go:119, which is the closure working;
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
// sweptRoutes decides "reached" by RouteFact.Matches and nothing else; the row
// below runs the insertion over every position and compares for EQUALITY. That
// is a handful of lines in one place, all of them named in this comment, and it
// is the same exposure as trusting that this file is compiled into the package
// at all.
//
// EQUALITY IS LOAD-BEARING AND NOT TIDINESS. Self-attacked, and it worked:
// weakening it to containment — assert.Subset, "the answer includes the
// inserted route" — keeps every self-test property and silently drops the real
// one, and a route this driver ships that nothing reaches then rides along in
// the answer at exit 0. Measured. That line is the floor; a further rung would
// be a closure over the closure with its own invocation to guard, and that
// regress does not terminate.

// describe is how a route is named in a complaint: what it is, and the line
// that introduced it.
func describe(r github.RouteFact) string {
	return fmt.Sprintf("%s %s, declared at %s", r.Method, r.Template, r.Declared)
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
func aRouteNothingReaches() github.RouteFact {
	return github.RouteFact{
		Method:   http.MethodGet,
		Template: "repos/{}/{}/nothing-reaches-this",
		Params:   []string{"page", decoyUnsent},
		Declared: decoyDeclared,
		Ships:    true,
		Matches:  func(string, string) bool { return false },
	}
}

// withRouteAt is the table with one more route in it, at the given position.
func withRouteAt(table []github.RouteFact, i int, r github.RouteFact) []github.RouteFact {
	return slices.Insert(slices.Clone(table), i, r)
}

// unreachedRoutes IS THE CLOSURE over the routing table: every route this
// driver ships that the sweep never reached, as one complaint each.
func unreachedRoutes(table []github.RouteFact, reached map[string]bool) []string {
	out := []string{}
	for _, r := range table {
		// A route a test built to drive the plumbing is not a request this
		// driver can issue.
		if !r.Ships || reached[r.Declared] {
			continue
		}
		out = append(out, describe(r))
	}
	return out
}

// sweptRoutes is the observation the closure is applied to: which routes OF
// THIS TABLE the sweep's request shapes reached.
func sweptRoutes(t *testing.T, table []github.RouteFact, shapes map[string][]string) map[string]bool {
	t.Helper()
	reached := map[string]bool{}
	for _, op := range sweepOps {
		for _, shape := range shapes[op.name] {
			for _, r := range routesMatching(t, table, shape) {
				reached[r.Declared] = true
			}
		}
	}
	return reached
}

// parametersNeverSent is the same closure one level down: every query parameter
// a shipping route may carry that the sweep never saw one of.
func parametersNeverSent(table []github.RouteFact, sent map[string]bool) []string {
	out := []string{}
	for _, r := range table {
		if !r.Ships {
			continue
		}
		for _, param := range r.Params {
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
	subject := newSweepForge(t, sweepOps[0])

	table := github.DeclaredRoutes(subject.drv)
	require.NotEmpty(t, table, "this driver declared no routes, so nothing here is a closure over anything")

	decoy := aRouteNothingReaches()
	want := []string{describe(decoy)}
	for i := 0; i <= len(table); i++ {
		with := withRouteAt(table, i, decoy)
		assert.Equalf(t, want, unreachedRoutes(with, sweptRoutes(t, with, shapes)),
			"with a route nothing reaches at position %d of %d, this closure must name it and nothing else. An extra line is a request this driver can issue that the sweep never reached — no redirect row, no anonymous row and no escaping row says anything about it. An empty answer is the closure not running over that position at all, which would arm a route there with nothing",
			i, len(table))
	}
}

// TestEveryQueryParameterThisDriverDeclaresIsSwept is the same closure one
// level down. A branch that adds a parameter to a request the sweep already
// reaches changes what is asked without changing any path — so the route
// declares which parameters it may carry, send refuses one it did not declare,
// and a declared parameter nothing sent is a parameter armed by nothing.
//
// It is applied the same way, for the reason the comment above gives: a loop
// with an assert in it is switched off by one token and says nothing about it.
func TestEveryQueryParameterThisDriverDeclaresIsSwept(t *testing.T) {
	subject := newSweepForge(t, sweepOps[0])

	sent := map[string]bool{}
	for _, op := range sweepOps {
		s := newSweepForge(t, op)
		_, err := op.call(ctx(), s.reg, s.sess, s.key, benignArgs)
		require.NoErrorf(t, err, "%s failed against a forge that answers everything correctly", op.name)
		for _, h := range s.wire.sent() {
			for name := range parsedQuery(t, h.query) {
				sent[name] = true
			}
		}
	}

	table := github.DeclaredRoutes(subject.drv)
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
// a route only constrains a request that was built through send — so a second
// place that builds one is a request outside the table, armed by nothing and
// escaped by nobody.
//
// It reads the package's own source rather than trusting a comment, because
// "send is the only place" is exactly the kind of sentence that stays in a file
// after it has stopped being true.
func TestThisDriverBuildsAnHTTPRequestInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/v2/forge/github")
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
		"this driver builds or issues an HTTP request somewhere other than send, so the routing table does not cover every request it makes")
	for _, where := range builders {
		assert.Contains(t, where, "api.go",
			"the one place a request is built is send, in api.go, and this is not it: %s", where)
	}
}

// TestThisDriverTurnsAStringIntoAPathInExactlyOnePlace is the census the
// traversal refusal rests on, made a fact of the program rather than a sentence
// in a comment.
//
// escapedSegments says of itself that it IS THE ONE PLACE A STRING BECOMES PART
// OF A PATH IN THIS PACKAGE, and until now nothing said so but the comment —
// which is exactly the kind of sentence that stays in a file after it has
// stopped being true. It matters more now than when it was only about escaping:
// addressesItself refuses a segment that names something else, and a second
// place that composed a path would be a place that refuses nothing.
//
// url.PathEscape and url.QueryEscape are the two ways a string is turned into a
// piece of a URL, and both are counted. What is asserted is the FILE: route.go is
// where escapedSegments and addressesItself live, and a call from anywhere else
// is a path composed outside the refusal — a call site that "escaped it here"
// and never asked whether the thing it escaped was a name.
// The load is written plainly, like its neighbour above, and it is hermetic
// anyway: the conformance suite this driver's tests link pins GOPROXY=off and
// GOWORK=off on the process in an init, and an environment is a property of a
// process rather than of a call site. Measured, from this package's directory
// with GOWORK naming a file that is not a workspace: both loads here pass.
func TestThisDriverTurnsAStringIntoAPathInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/v2/forge/github")
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	pkg := loaded[0]
	require.Emptyf(t, pkg.Errors, "type-checking this driver's own source: %v", pkg.Errors)

	escapers := []string{}
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
			fn, ok := pkg.TypesInfo.Uses[sel.Sel].(interface{ FullName() string })
			if !ok {
				return true
			}
			switch fn.FullName() {
			case "net/url.PathEscape", "net/url.QueryEscape":
				escapers = append(escapers, pkg.Fset.Position(call.Pos()).String()+" "+fn.FullName())
			}
			return true
		})
	}

	t.Logf("this driver turns a string into a piece of a path at %v", escapers)
	require.NotEmptyf(t, escapers, "this driver escapes nothing, so this row is a closure over nothing")
	for _, where := range escapers {
		assert.Containsf(t, where, "route.go",
			"a string becomes part of a path somewhere other than escapedSegments, which is where the refusal is: %s", where)
	}
}
