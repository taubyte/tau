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
	"go/ast"
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
	method, path, found := strings.Cut(shape, " ")
	require.Truef(t, found, "%q is not a request shape", shape)

	matched := []github.RouteFact{}
	for _, r := range github.DeclaredRoutes(drv) {
		if r.Matches(method, path) {
			matched = append(matched, r)
		}
	}
	assert.Lenf(t, matched, 1, "%q matched %d routes; every request this driver makes is one route's", shape, len(matched))
	return matched
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

	reached := map[string]bool{}
	for _, op := range sweepOps {
		for _, shape := range shapes[op.name] {
			for _, r := range assertOneRoute(t, subject.drv, shape) {
				reached[r.Declared] = true
			}
		}
	}

	table := github.DeclaredRoutes(subject.drv)
	require.NotEmpty(t, table, "this driver declared no routes, so nothing here is a closure over anything")
	for _, r := range table {
		if !r.Ships {
			// A route a test built to drive the plumbing is not a request this
			// driver can issue.
			continue
		}
		assert.Truef(t, reached[r.Declared],
			"%s %s, declared at %s, is a request this driver can issue and the sweep never reached it: no redirect row, no anonymous row and no escaping row says anything about it",
			r.Method, r.Template, r.Declared)
	}
}

// TestEveryQueryParameterThisDriverDeclaresIsSwept is the same closure one
// level down. A branch that adds a parameter to a request the sweep already
// reaches changes what is asked without changing any path — so the route
// declares which parameters it may carry, send refuses one it did not declare,
// and a declared parameter nothing sent is a parameter armed by nothing.
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

	for _, r := range github.DeclaredRoutes(subject.drv) {
		if !r.Ships {
			continue
		}
		for _, param := range r.Params {
			assert.Truef(t, sent[param],
				"%s %s, declared at %s, may carry the query parameter %q and the sweep never saw one",
				r.Method, r.Template, r.Declared, param)
		}
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
