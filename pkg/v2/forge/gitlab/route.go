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

// THE PATH OF A REQUEST IS A TEMPLATE FROM A CLOSED SET, NEVER A STRING A CALL
// SITE PASTED TOGETHER. The github driver in this tree says the same thing in
// the same place, because a rule two drivers hold has to be held the same way
// or the difference between them is a difference nobody can read.
//
// THE ESCAPING was already one function here, and it stays one: endpoint()
// escaped every segment it was given, which is why deleting that escape fails
// six named tests while five of the github driver's seven could be deleted at
// exit 0. What that mechanism did not cover is the URL this driver hands BACK —
// CloneAddress pasted key.FullName() into a clone address raw — so the function
// below is now what both use.
//
// WHAT IS NEW IS DISCOVERY. The sweep arms every request this driver makes by
// running every operation and reading what arrived, so a request only some
// input reaches is a request the sweep never sees. A route is registered by
// being CONSTRUCTED, so the set of requests this driver can issue is a fact of
// the program rather than a list somebody keeps, and the sweep fails on a route
// it did not reach rather than silently not covering it.
//
// A QUERY PARAMETER IS DECLARED THE SAME WAY, for the same reason: a branch
// that adds one to a request the sweep already reaches would otherwise change
// what is asked without changing any path the sweep compares.
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
	// ships says this route was declared in code this driver ships, as opposed
	// to a route a test builds to drive the plumbing. It is Go's own division —
	// a _test.go file is not in the package a consumer imports — and it is
	// recorded here rather than recovered from the string above.
	ships bool
}

// hole is the template segment a call fills in.
const hole = "{}"

var (
	routesMu   sync.Mutex
	routeTable []*route
	enrolled   = map[*route]bool{}
)

// newRoute declares one request this driver can issue, and REGISTERS IT BY
// CONSTRUCTING IT. There is no second list to keep in step.
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

// THE ROUTING TABLE. Everything this driver asks a GitLab instance is here.
//
// A PROJECT IS ADDRESSED BY ITS FULL PATH, URL-ENCODED, which is why the hole
// under projects/ takes acme/team/widget and the escaping below turns it into
// acme%2Fteam%2Fwidget. That single hole is GitLab's own addressing and not a
// flattening: the "/" inside it is data, and it must arrive as %2F or the
// request lands on a different route entirely.
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

	routeRegisterPushHook = newRoute(http.MethodPost, "api/v4/projects/"+hole+"/hooks")
	routeRemovePushHook   = newRoute(http.MethodDelete, "api/v4/projects/"+hole+"/hooks/"+hole)

	// The two the DEPLOYMENT's own credential spends, on a client no session
	// can reach.
	routeUser        = newRoute(http.MethodGet, "api/v4/users", "username")
	routeGroupMember = newRoute(http.MethodGet, "api/v4/groups/"+hole+"/members/all/"+hole)
)

// request is one route with the arguments its holes take.
type request struct {
	route *route
	args  []string
	query url.Values
}

// at fills a route's holes, in order.
func (r *route) at(args ...string) request { return request{route: r, args: args} }

// with adds query parameters, which the route must have declared.
func (rq request) with(query url.Values) request {
	rq.query = query
	return rq
}

// escapedSegments IS THE ONE PLACE A STRING BECOMES PART OF A PATH IN THIS
// PACKAGE. The routes above go through it, and so does the clone address, which
// is not a request at all but is still a URL with a key pasted into it.
//
// It escapes literals too: url.PathEscape("projects") is "projects", so the
// exception would buy nothing and would cost the property — there is no segment
// here that somebody has to decide about.
//
// AND ESCAPING WAS NEVER THE PROPERTY, which is the correction. See
// addressesItself: a segment that is escaped perfectly can still be a segment
// that names something else, and this function refused nothing.
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

// addressesItself IS THE PROPERTY ESCAPING DOES NOT GIVE: a segment must still
// name the place it was put in after a resolver has read it. The github driver
// in this tree states it in the same place and the same words, because a rule
// two drivers hold has to be held the same way or the difference between them is
// a difference nobody can read.
//
// url.PathEscape("..") is "..". A dot is unreserved, so escaping leaves a dot
// segment exactly as it was — and a dot segment is not a name, it is an
// instruction to whoever resolves the path. RFC 3986 remove_dot_segments is
// required of every conforming proxy, gateway and origin, and Go's own
// path.Clean and URL.ResolveReference implement it, so this driver composing
//
//	DELETE /api/v4/projects/acme%2Fsite/deploy_keys/..
//
// is this driver composing DELETE /api/v4/projects/acme%2Fsite — deleting the
// project it was asked to tidy — with the caller's credential already attached.
// "." is the same defect one rung down: it resolves to the COLLECTION, which is
// the threat handleSegment was already written against.
//
// IT IS CHECKED IN THE RAW SPELLING, WHICH IS THE ONE THE SERVER HOLDS. This
// driver escapes once and the instance on the other end decodes once, so the
// string a server really has in its hands is the segment as it arrived here —
// which is why "../.." is refused even though it escapes to "..%2F..", one
// innocent-looking segment on the wire.
//
// AND CHECKING THE ESCAPED SPELLING AS WELL WOULD BE CHECKING THIS ONE TWICE.
// It was written that way and it was dead: measured, deleting the escaped half
// left the whole forge tree at GO_TEST_EXIT=0, because url.PathEscape emits no
// "/" and leaves an unreserved dot alone, so PathEscape(s) is a dot segment
// exactly when s is. That coincidence is not left as an argument —
// TestCheckingTheEscapedSpellingWouldBeCheckingTheRawOneTwice is the row that
// holds it, and it is what this one line rests on.
//
// A "/" INSIDE A SEGMENT IS NOT REFUSED, and on this driver it may not be: the
// hole under projects/ takes acme/team/widget and the escaping turns it into
// acme%2Fteam%2Fwidget, which is GitLab's own addressing. What is refused is a
// COMPONENT of that path that is a dot segment, which no project path has.
//
// IT IS NOT AIMED AT ESCAPING, and it must not be. An instance hands back an
// opaque handle and may legitimately spell one with a "?", a "#" or a "%" in it;
// those are escaped here and arrive as themselves, and refusing them would
// refuse real instances. What is refused is the segment that, escaped perfectly,
// still addresses something the caller never named.
//
// The one decode is the doctrine. A server that decodes twice turns "%252e" back
// into "." and no escaping survives that; it is a defect in the server, and a
// driver that tried to pre-empt it would be refusing every handle with a percent
// in it for a spelling nothing conforming produces.
func addressesItself(segment string) error {
	if segment == "" {
		return errors.New("an empty path segment addresses the collection the request is under, so one thing is asked about and all of them are answered")
	}
	if path.Clean("/"+segment) != "/"+segment {
		return fmt.Errorf("%q is not a name: a resolver removing dot segments does not leave it where this driver put it, so the request would address something the caller never named", bounded(segment))
	}
	return nil
}

// bounded cuts a string SOMEBODY ELSE CHOSE to a length an error may carry, for
// the reason maxHostString gives: whoever picks how long a log line is picks how
// much of a disk it costs.
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
