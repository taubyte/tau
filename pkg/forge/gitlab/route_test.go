package gitlab

// A path is a route declared by newRoute, so the requests this driver can issue
// are a fact of the program; a route nothing reached fails here.

import (
	"context"
	"fmt"
	"go/ast"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// assertOneRoute is the other half of the table: a request that matches no
// route is a path composed outside it, and a request that matches two is a
// table in which "this route was reached" can be satisfied by another route's
// traffic.
func assertOneRoute(t *testing.T, cfg *config, shape string) []*route {
	t.Helper()
	return routesMatching(t, cfg, declaredRoutes(), shape)
}

// routesMatching asks one table, not the package's own: the closures below walk
// a table with a route inserted into it.
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

func declaredRoutes() []*route {
	routesMu.Lock()
	defer routesMu.Unlock()
	return append([]*route(nil), routeTable...)
}

// The row inserts a route nothing reaches at every position and requires the
// complaints to be exactly that route's: none means the closure did not run, an
// extra one is a shipped route the sweep missed. Equality is load-bearing:
// containment would let a shipped, unreached route ride along.

func describe(r *route) string {
	return fmt.Sprintf("%s %s, declared at %s", r.method, r.template, r.declared)
}

// decoyDeclared is where the route nothing reaches says it comes from. It is
// prose rather than a file:line, because a reader who sees it in a failure has
// to be able to tell it from a route this driver really declared.
const decoyDeclared = "route_test.go, as the route nothing reaches"

const decoyUnsent = "nothing-sends-this"

// aRouteNothingReaches ships, since a route a test built is skipped by both
// closures. It carries "page", which the sweep sends, and decoyUnsent, which
// nothing sends.
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

func withRouteAt(table []*route, i int, r *route) []*route {
	return slices.Insert(slices.Clone(table), i, r)
}

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

func TestThisDriverBuildsAnHTTPRequestInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/forge/gitlab")
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
		"this driver builds or issues an HTTP request somewhere other than do, so the routing table does not cover every request it makes")
	for _, where := range builders {
		assert.Contains(t, where, "http.go",
			"the one place a request is built is do, in http.go, and this is not it: %s", where)
	}
}

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

func TestACloneAddressOfAKeyWithNoOwner(t *testing.T) {
	d, err := New("gitlab.example")
	require.NoError(t, err)
	bound, err := d.Bind(forge.Token("tok"))
	require.NoError(t, err)

	addr, err := bound.(forge.CloneAddresses).CloneAddress(repokey.Key{Host: "gitlab.example", Name: "pay"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.example/pay.git", addr.URL)
}

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

func TestEveryHoleOfEveryRouteRefusesASegmentThatNamesSomethingElse(t *testing.T) {
	for _, r := range declaredRoutesForTest() {
		holes := strings.Count(r.template, hole)
		if holes == 0 {
			continue
		}
		t.Run(r.method+" "+r.template, func(t *testing.T) {
			for at := 0; at < holes; at++ {
				for _, spelling := range []string{"", "..", ".", "../..", "a/.."} {
					args := make([]string, holes)
					for i := range args {
						args[i] = "benign"
					}
					args[at] = spelling

					_, err := composeForTest(r.at(args...))
					assert.Errorf(t, err,
						"hole %d of %s %s took %q and composed a path anyway", at, r.method, r.template, spelling)
				}
			}

			benign := make([]string, holes)
			for i := range benign {
				benign[i] = "benign"
			}
			_, err := composeForTest(r.at(benign...))
			assert.NoErrorf(t, err, "%s %s refuses a name, so the refusals above prove nothing", r.method, r.template)
		})
	}
}

func declaredRoutesForTest() []*route {
	routesMu.Lock()
	defer routesMu.Unlock()
	return append([]*route(nil), routeTable...)
}

func composeForTest(rq request) (string, error) {
	base, err := url.Parse("https://gitlab.example")
	if err != nil {
		return "", err
	}
	u, err := rq.url(base)
	if err != nil {
		return "", err
	}
	return u.EscapedPath(), nil
}

func TestCheckingTheEscapedSpellingWouldBeCheckingTheRawOneTwice(t *testing.T) {
	survives := func(s string) bool { return path.Clean("/"+s) == "/"+s }

	alphabet := []string{".", "/", "%", "2", "e", "F", "a", "?"}
	visited, held, converseFailed := 0, 0, 0

	var walk func(string, int)
	walk = func(s string, left int) {
		visited++
		if survives(s) {
			held++
			require.Truef(t, survives(url.PathEscape(s)),
				"%q survives dot-segment removal and %q, which is what this driver actually sends, does not: the escaped spelling is not implied by the raw one",
				s, url.PathEscape(s))
		} else if survives(url.PathEscape(s)) {
			converseFailed++
		}
		if left == 0 {
			return
		}
		for _, c := range alphabet {
			walk(s+c, left-1)
		}
	}
	walk("", 3)

	// 1 + 8 + 8*8 + 8*8*8: the empty string and every string of one, two and
	// three characters over the alphabet above.
	require.Equalf(t, 1+8+64+512, visited,
		"this row claims to be exhaustive over an eight-character alphabet up to length three, and it walked %d strings", visited)

	t.Logf("%d spellings survive raw and every one of them survives escaped", held)
	require.Positivef(t, converseFailed,
		"no spelling survives escaped and fails raw, so the two checks are the same check and the raw one is not the stronger: %d held", held)
	t.Logf("%d spellings survive escaped and do NOT survive raw, which is why the raw one is the one kept", converseFailed)
}

func TestThisDriverTurnsAStringIntoAPathInExactlyOnePlace(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: false,
	}, "github.com/taubyte/tau/pkg/forge/gitlab")
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
