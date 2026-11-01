package gitlab

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

	"github.com/taubyte/tau/pkg/git/repokey"
)

// A request path is a template from a closed set, as in the github driver: every
// segment, the clone address's included, goes through escapedSegments, and a
// route is registered by being constructed, so the sweep fails on a route it
// never reached. Query parameters are declared the same way.
type route struct {
	method string
	// template is the path under the instance root, "/"-separated, with hole
	// for each segment a call fills in.
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

const hole = "{}"

var (
	routesMu   sync.Mutex
	routeTable []*route
	enrolled   = map[*route]bool{}
)

// newRoute registers a route by constructing it, so there is no second list to
// keep in step.
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

// The routing table. A project is addressed by its full path URL-encoded: the
// hole under projects/ takes acme/team/widget, and its "/" must arrive as %2F.
var (
	routeCurrentUser = newRoute(http.MethodGet, "api/v4/user")

	// Not under /api/v4, and the only request this driver makes that is not.
	routeAuthorization = newRoute(http.MethodPost, "api/graphql")

	routeProject       = newRoute(http.MethodGet, "api/v4/projects/"+hole)
	routeDeleteProject = newRoute(http.MethodDelete, "api/v4/projects/"+hole)
	routeListProjects  = newRoute(http.MethodGet, "api/v4/projects",
		"membership", "per_page", "page", "order_by", "sort")
	routeCreateProject = newRoute(http.MethodPost, "api/v4/projects")
	routeNamespace     = newRoute(http.MethodGet, "api/v4/namespaces/"+hole)

	routeInstallDeployKey = newRoute(http.MethodPost, "api/v4/projects/"+hole+"/deploy_keys")
	routeRemoveDeployKey  = newRoute(http.MethodDelete, "api/v4/projects/"+hole+"/deploy_keys/"+hole)
	routeListDeployKeys   = newRoute(http.MethodGet, "api/v4/projects/"+hole+"/deploy_keys", "per_page", "page")

	routeRegisterPushHook = newRoute(http.MethodPost, "api/v4/projects/"+hole+"/hooks")
	routeRemovePushHook   = newRoute(http.MethodDelete, "api/v4/projects/"+hole+"/hooks/"+hole)
	routeListPushHooks    = newRoute(http.MethodGet, "api/v4/projects/"+hole+"/hooks", "per_page", "page")

	// Spent with the deployment's own credential.
	routeUser        = newRoute(http.MethodGet, "api/v4/users", "username")
	routeGroupMember = newRoute(http.MethodGet, "api/v4/groups/"+hole+"/members/all/"+hole)
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
			return "", fmt.Errorf("gitlab: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself refuses a segment that would not name the place it was put
// once resolved: url.PathEscape("..") is "..", and DELETE .../deploy_keys/..
// resolves to deleting the project; "." resolves to the collection. It checks
// the raw spelling, the one the server holds after its one decode, so "../.."
// is refused though it escapes to one segment; checking the escaped spelling
// too would check this one twice. A "/" inside a segment is GitLab's own
// addressing and is not refused. A "?", "#" or "%" is escaped and sent.
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

// keySegments is a key's path on this instance, as the segments a URL under the
// host really has: every group in the owner, then the project. It is what a
// CLONE address is built from — the "/" between groups is punctuation there,
// where under projects/ the whole path is one encoded argument.
func keySegments(key repokey.Key) []string {
	if key.Owner == "" {
		return []string{key.Name}
	}
	return append(strings.Split(key.Owner, "/"), key.Name)
}

// segments is the path this request names, unescaped, or the reason it is not
// one.
func (rq request) segments() ([]string, error) {
	r := rq.route
	switch {
	case r == nil:
		return nil, errors.New("gitlab: a request with no route names nothing")
	case !registered(r):
		// A route value built any way other than by newRoute is a request this
		// driver can issue that the sweep was never told about.
		return nil, fmt.Errorf("gitlab: %s %s was not declared by newRoute, so nothing arms it", r.method, r.template)
	}

	out := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			out = append(out, s)
			continue
		}
		if next == len(rq.args) {
			return nil, fmt.Errorf("gitlab: %s %s takes %d arguments and was given %d", r.method, r.template, strings.Count(r.template, hole), len(rq.args))
		}
		out = append(out, rq.args[next])
		next++
	}
	if next != len(rq.args) {
		return nil, fmt.Errorf("gitlab: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return out, nil
}

// url is the absolute URL this request goes to. Path carries the segments as
// they are and RawPath carries them escaped, which is how net/url is told that
// a "/" inside a segment is data.
func (rq request) url(base *url.URL) (*url.URL, error) {
	segments, err := rq.segments()
	if err != nil {
		return nil, err
	}
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return nil, fmt.Errorf("gitlab: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
	}

	escaped, err := escapedSegments(segments)
	if err != nil {
		return nil, err
	}

	u := *base
	u.Path = "/" + strings.Join(segments, "/")
	u.RawPath = "/" + escaped
	u.RawQuery = rq.query.Encode()
	return &u, nil
}

func registered(r *route) bool {
	routesMu.Lock()
	defer routesMu.Unlock()
	return enrolled[r]
}

// matches reports whether one request that went out is this route's. Literals
// must be equal, holes take anything, and the instance root is the prefix.
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
