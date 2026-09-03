package github

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

// THE PATH OF A REQUEST IS A TEMPLATE FROM A CLOSED SET, NEVER A STRING A CALL
// SITE PASTED TOGETHER. Two defects live in the difference.
//
// THE FIRST IS ESCAPING. A path assembled with "+" has one escape per author
// per line, and seven of them here were seven separate chances to forget: five
// could be deleted individually with the whole forge tree still at exit 0.
// Below, a segment reaches a path through exactly one function, which escapes
// it — the literals this package wrote included, because an exception is a rule
// somebody has to remember and a literal escapes to itself anyway.
//
// THE SECOND IS DISCOVERY, and it is why this is a TABLE and not just a helper.
// The sweep in this package's tests arms every request this driver makes by
// running every operation and reading what arrived; a request that only some
// input reaches — the personal-account create endpoint, armed by nothing for
// five rounds, or a branch added tomorrow — is a request the sweep never sees
// and therefore never arms. A route is registered by being CONSTRUCTED, so the
// set of requests this driver can issue is a fact of the program rather than a
// list somebody keeps, and the sweep fails on a route it did not reach instead
// of silently not covering it.
//
// A QUERY PARAMETER IS DECLARED THE SAME WAY, for the same reason: a branch
// that adds one to a request the sweep already reaches would otherwise change
// what is asked without changing any shape the sweep compares.
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
	// ships says this route was declared in code this driver ships, as opposed
	// to a route a test builds to drive the plumbing. It is Go's own division —
	// a _test.go file is not in the package a consumer imports — and it is
	// recorded here rather than recovered from the string above.
	ships bool
}

// hole is the template segment a call fills in. It is deliberately not a
// name — a named placeholder is a second spelling of the argument order, and
// the two drift.
const hole = "{}"

var (
	routesMu   sync.Mutex
	routeTable []*route
	enrolled   = map[*route]bool{}
)

// newRoute declares one request this driver can issue, and REGISTERS IT BY
// CONSTRUCTING IT. There is no second list to keep in step: a route that exists
// is a route the sweep must reach, and send refuses to issue one that was built
// any other way.
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

// THE ROUTING TABLE. Everything this driver asks GitHub is here, and the whole
// of it is thirteen requests — which is the number the package comment's "no
// SDK" argument rests on, now countable rather than asserted.
var (
	routeCurrentUser = newRoute(http.MethodGet, "user")

	// The listing and the personal create are the same path under two methods,
	// which is exactly why an operation is not a unit: they are two requests.
	routeListRepositories = newRoute(http.MethodGet, "user/repos", "per_page", "page")
	routeCreateOwnedByMe  = newRoute(http.MethodPost, "user/repos")
	routeCreateUnderOrg   = newRoute(http.MethodPost, "orgs/"+hole+"/repos")

	routeRepository       = newRoute(http.MethodGet, "repos/"+hole+"/"+hole)
	routeDeleteRepository = newRoute(http.MethodDelete, "repos/"+hole+"/"+hole)

	routeInstallDeployKey = newRoute(http.MethodPost, "repos/"+hole+"/"+hole+"/keys")
	routeRemoveDeployKey  = newRoute(http.MethodDelete, "repos/"+hole+"/"+hole+"/keys/"+hole)

	routeRegisterPushHook = newRoute(http.MethodPost, "repos/"+hole+"/"+hole+"/hooks")
	routeRemovePushHook   = newRoute(http.MethodDelete, "repos/"+hole+"/"+hole+"/hooks/"+hole)

	// The three the DEPLOYMENT's own credential spends, on a client no session
	// can reach.
	routeInstallation      = newRoute(http.MethodGet, "orgs/"+hole+"/installation")
	routeInstallationToken = newRoute(http.MethodPost, "app/installations/"+hole+"/access_tokens")
	routeMembership        = newRoute(http.MethodGet, "orgs/"+hole+"/memberships/"+hole)
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
// PACKAGE. Every caller — the routes above and the clone address, which is not
// a request at all but is still a URL with a key pasted into it — goes through
// here, so "was this escaped" is one question with one answer rather than one
// question per line.
//
// It escapes literals too. url.PathEscape("repos") is "repos", so the exception
// would buy nothing and would cost the property: there is no segment here that
// somebody has to decide about.
//
// AND ESCAPING WAS NEVER THE PROPERTY, which is the correction. See
// addressesItself: a segment that is escaped perfectly can still be a segment
// that names something else, and this function refused nothing.
func escapedSegments(segments []string) (string, error) {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		if err := addressesItself(s); err != nil {
			return "", fmt.Errorf("forge/github: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself IS THE PROPERTY ESCAPING DOES NOT GIVE: a segment must still
// name the place it was put in after a resolver has read it.
//
// url.PathEscape("..") is "..". A dot is unreserved, so escaping leaves a dot
// segment exactly as it was — and a dot segment is not a name, it is an
// instruction to whoever resolves the path. RFC 3986 remove_dot_segments is
// required of every conforming proxy, gateway and origin, and Go's own
// path.Clean and URL.ResolveReference implement it, so this driver composing
//
//	DELETE /api/v3/repos/acme/site/keys/..
//
// is this driver composing DELETE /api/v3/repos/acme/site — deleting the
// repository it was asked to tidy — with the caller's credential already
// attached. "." is the same defect one rung down: it resolves to the
// COLLECTION, which is the threat handleSegment was already written against.
//
// IT IS CHECKED IN THE RAW SPELLING, WHICH IS THE ONE THE SERVER HOLDS. This
// driver escapes once and the forge on the other end decodes once, so the string a
// server really has in its hands is the segment as it arrived here — which is
// why "../.." is refused even though it escapes to "..%2F..", one
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
// IT IS NOT AIMED AT ESCAPING, and it must not be. A forge hands back an opaque
// handle and may legitimately spell one with a "?", a "#" or a "%" in it; those
// are escaped here and arrive as themselves, and refusing them would refuse
// real forges. What is refused is the segment that, escaped perfectly, still
// addresses something the caller never named.
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

// path is the escaped path this request names, or the reason it is not one.
func (rq request) path() (string, error) {
	r := rq.route
	switch {
	case r == nil:
		return "", errors.New("forge/github: a request with no route names nothing")
	case !registered(r):
		// A route value built any way other than by newRoute is a request this
		// driver can issue that the sweep was never told about.
		return "", fmt.Errorf("forge/github: %s %s was not declared by newRoute, so nothing arms it", r.method, r.template)
	}

	segments := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			segments = append(segments, s)
			continue
		}
		if next == len(rq.args) {
			return "", fmt.Errorf("forge/github: %s %s takes %d arguments and was given %d", r.method, r.template, strings.Count(r.template, hole), len(rq.args))
		}
		segments = append(segments, rq.args[next])
		next++
	}
	if next != len(rq.args) {
		return "", fmt.Errorf("forge/github: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return escapedSegments(segments)
}

// rawQuery is the encoded query, refusing a parameter the route does not
// declare.
func (rq request) rawQuery() (string, error) {
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/github: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
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
// with is the prefix — which is what makes an enterprise instance's /api/v3 and
// api.github.com's root the same table.
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
