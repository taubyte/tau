package github_test

// A path is a route declared by newRoute, so the requests this driver can issue
// are a fact of the program; a route nothing reached fails here, naming the line
// that declared it.

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

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
)

// assertOneRoute is the other half of the table: a request that matches no
// route is a path composed outside it, and a request that matches two is a
// table in which "this route was reached" can be satisfied by another route's
// traffic.
func assertOneRoute(t *testing.T, drv forge.Driver, shape string) []github.RouteFact {
	t.Helper()
	return routesMatching(t, github.DeclaredRoutes(drv), shape)
}

// routesMatching asks one table, not the driver's own: the closures below walk a
// table with a route inserted into it.
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

// The row inserts a route nothing reaches at every position and requires the
// complaints to be exactly that route's: none means the closure did not run, an
// extra one is a shipped route the sweep missed. Equality is load-bearing:
// containment would let a shipped, unreached route ride along.

func describe(r github.RouteFact) string {
	return fmt.Sprintf("%s %s, declared at %s", r.Method, r.Template, r.Declared)
}

// decoyDeclared is where the route nothing reaches says it comes from. It is
// prose rather than a file:line, because a reader who sees it in a failure has
// to be able to tell it from a route this driver really declared.
const decoyDeclared = "route_test.go, as the route nothing reaches"

const decoyUnsent = "nothing-sends-this"

// aRouteNothingReaches ships, since a route a test built is skipped by both
// closures. It carries "page", which the sweep sends, and decoyUnsent, which
// nothing sends.
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

func withRouteAt(table []github.RouteFact, i int, r github.RouteFact) []github.RouteFact {
	return slices.Insert(slices.Clone(table), i, r)
}

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

func TestThisDriverBuildsAnHTTPRequestInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/forge/github")
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

	// One place builds the client: the pin and the redirect policy live on an
	// http.Client, so a second client is a second set of both.
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

func TestThisDriverTurnsAStringIntoAPathInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/forge/github")
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
