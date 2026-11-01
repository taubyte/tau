package bitbucket

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// A request path is a template from a closed set, never pasted together: every
// segment is escaped by escapedSegments, and a route is registered by being
// constructed, so the sweep fails on a route it never reached. Query parameters
// are declared the same way.
type route struct {
	method string
	// template is the path under the API base, "/"-separated, with hole for
	// each segment a call fills in.
	template string
	segments []string
	// params are the query parameters this route may carry. A request naming
	// one this route does not is refused rather than sent.
	params []string
	// declared is where newRoute was called, so a route the sweep never reached
	// is reported by the line that introduced it.
	declared string
	// ships says the route was declared outside a _test.go file, not built by a
	// test to drive the plumbing.
	ships bool
}

// hole is not a name: a named placeholder is a second spelling of the argument
// order, and the two drift.
const hole = "{}"

var (
	routesMu   sync.Mutex
	routeTable []*route
	enrolled   = map[*route]bool{}
)

// newRoute registers a route by constructing it, so there is no second list to
// keep in step; send refuses a route built any other way.
func newRoute(method, template string, params ...string) *route {
	r := &route{
		method:   method,
		template: template,
		segments: strings.Split(template, "/"),
		params:   params,
		declared: "unknown",
	}
	if _, file, line, ok := runtime.Caller(1); ok {
		r.declared = fmt.Sprintf("%s:%d", filepath.Base(file), line)
		r.ships = !strings.HasSuffix(file, "_test.go")
	}
	routesMu.Lock()
	defer routesMu.Unlock()
	routeTable = append(routeTable, r)
	enrolled[r] = true
	return r
}

// The routing table: everything this driver asks Bitbucket. "role" scopes the
// global listing to repositories the credential is a member of; "q" filters
// the permission lookup.
var (
	routeCurrentUser = newRoute(http.MethodGet, "user")

	routeListRepositories = newRoute(http.MethodGet, "repositories", "role", "pagelen", "page")
	routePermission       = newRoute(http.MethodGet, "user/permissions/repositories", "q")

	routeRepository       = newRoute(http.MethodGet, "repositories/"+hole+"/"+hole)
	routeCreateRepository = newRoute(http.MethodPost, "repositories/"+hole+"/"+hole)
	routeDeleteRepository = newRoute(http.MethodDelete, "repositories/"+hole+"/"+hole)

	routeInstallDeployKey = newRoute(http.MethodPost, "repositories/"+hole+"/"+hole+"/deploy-keys")
	routeRemoveDeployKey  = newRoute(http.MethodDelete, "repositories/"+hole+"/"+hole+"/deploy-keys/"+hole)
	routeListDeployKeys   = newRoute(http.MethodGet, "repositories/"+hole+"/"+hole+"/deploy-keys", "pagelen", "page")

	routeRegisterPushHook = newRoute(http.MethodPost, "repositories/"+hole+"/"+hole+"/hooks")
	routeRemovePushHook   = newRoute(http.MethodDelete, "repositories/"+hole+"/"+hole+"/hooks/"+hole)
	routeListPushHooks    = newRoute(http.MethodGet, "repositories/"+hole+"/"+hole+"/hooks", "pagelen", "page")
)

type request struct {
	route *route
	args  []string
	query url.Values
}

func (r *route) at(args ...string) request { return request{route: r, args: args} }

// with adds query parameters, which the route must have declared.
func (rq request) with(query url.Values) request {
	rq.query = query
	return rq
}

// escapedSegments is the one place a string becomes part of a path, literals
// included. Escaping is not enough; see addressesItself.
func escapedSegments(segments []string) (string, error) {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		if err := addressesItself(s); err != nil {
			return "", fmt.Errorf("forge/bitbucket: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself refuses a segment that would not name the place it was put
// once resolved: url.PathEscape("..") is "..", and DELETE .../deploy-keys/..
// resolves to deleting the repository; "." resolves to the collection. It checks
// the raw spelling, the one the server holds after its one decode, so "../.."
// is refused though it escapes to one segment; checking the escaped spelling
// too would check this one twice. A "?", "#" or "%" is escaped and sent: real
// forges spell handles with them.
func addressesItself(segment string) error {
	if segment == "" {
		return errors.New("an empty path segment addresses the collection the request is under, so one thing is asked about and all of them are answered")
	}
	if path.Clean("/"+segment) != "/"+segment {
		return fmt.Errorf("%q is not a name: a resolver removing dot segments does not leave it where this driver put it, so the request would address something the caller never named", bounded(segment))
	}
	return nil
}

// bounded cuts a string somebody else chose to a length an error may carry.
func bounded(s string) string {
	if len(s) <= maxHostString {
		return s
	}
	return s[:maxHostString] + "…"
}

func (rq request) path() (string, error) {
	r := rq.route
	switch {
	case r == nil:
		return "", errors.New("forge/bitbucket: a request with no route names nothing")
	case !registered(r):
		// A route value built any way other than by newRoute is a request this
		// driver can issue that the sweep was never told about.
		return "", fmt.Errorf("forge/bitbucket: %s %s was not declared by newRoute, so nothing arms it", r.method, r.template)
	}

	segments := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			segments = append(segments, s)
			continue
		}
		if next == len(rq.args) {
			return "", fmt.Errorf("forge/bitbucket: %s %s takes %d arguments and was given %d", r.method, r.template, strings.Count(r.template, hole), len(rq.args))
		}
		segments = append(segments, rq.args[next])
		next++
	}
	if next != len(rq.args) {
		return "", fmt.Errorf("forge/bitbucket: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return escapedSegments(segments)
}

// rawQuery is the encoded query, refusing a parameter the route does not
// declare.
func (rq request) rawQuery() (string, error) {
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/bitbucket: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
	}
	return rq.query.Encode(), nil
}

func registered(r *route) bool {
	routesMu.Lock()
	defer routesMu.Unlock()
	return enrolled[r]
}

// matches reports whether one request that went out is this route's. Literals
// must be equal, holes take anything, and the base path this driver was built
// with is the prefix, /2.0/ on every Bitbucket.
func (r *route) matches(base *url.URL, method, escapedPath string) bool {
	if method != r.method {
		return false
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(escapedPath, prefix) {
		return false
	}
	got := strings.Split(strings.TrimPrefix(escapedPath, prefix), "/")
	if len(got) != len(r.segments) {
		return false
	}
	for i, s := range r.segments {
		if s == hole {
			continue
		}
		// The literal is compared against what a server would decode, so this
		// is not a second escaper: escapedSegments stays the only one.
		decoded, err := url.PathUnescape(got[i])
		if err != nil || decoded != s {
			return false
		}
	}
	return true
}
