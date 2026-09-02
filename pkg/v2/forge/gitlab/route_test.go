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
	"go/ast"
	"net/http"
	"net/url"
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
	method, path, found := strings.Cut(shape, " ")
	require.Truef(t, found, "%q is not a request shape", shape)

	matched := []*route{}
	for _, r := range declaredRoutes() {
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

// TestEveryRouteThisDriverCanIssueIsSwept.
//
// This is the row a new branch fails. The sweep drives every operation; a route
// that no operation reached is a request this driver can issue that nothing in
// the sweep arms — not the redirect rows, not the anonymous row, not the
// escaping rows — and it is named here by the line that declared it rather than
// discovered a round later.
func TestEveryRouteThisDriverCanIssueIsSwept(t *testing.T) {
	shapes := sweepShapes(t)
	subject := newSweepInstance(t)

	reached := map[*route]bool{}
	for _, op := range sweepOps {
		for _, shape := range shapes[op.name] {
			for _, r := range assertOneRoute(t, subject.cfg, shape) {
				reached[r] = true
			}
		}
	}

	table := declaredRoutes()
	require.NotEmpty(t, table, "this driver declared no routes, so nothing here is a closure over anything")
	for _, r := range table {
		if !r.ships {
			// A route a test built to drive the plumbing is not a request this
			// driver can issue.
			continue
		}
		assert.Truef(t, reached[r],
			"%s %s, declared at %s, is a request this driver can issue and the sweep never reached it: no redirect row, no anonymous row and no escaping row says anything about it",
			r.method, r.template, r.declared)
	}
}

// TestEveryQueryParameterThisDriverDeclaresIsSwept is the same closure one
// level down. A branch that adds a parameter to a request the sweep already
// reaches changes what is asked without changing any path — so the route
// declares which parameters it may carry, do refuses one it did not declare,
// and a declared parameter nothing sent is a parameter armed by nothing.
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

	for _, r := range declaredRoutes() {
		if !r.ships {
			continue
		}
		for _, param := range r.params {
			assert.Truef(t, sent[param],
				"%s %s, declared at %s, may carry the query parameter %q and the sweep never saw one",
				r.method, r.template, r.declared, param)
		}
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
