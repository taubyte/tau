package onedev

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

// THE PATH OF A REQUEST IS A TEMPLATE FROM A CLOSED SET, the gitea driver's
// rule for its reasons: every segment reaches a path through one function that
// escapes it and refuses a dot segment, and a route is registered by being
// constructed, so the sweep in this package's tests can fail on a request no
// test reached instead of silently not covering it.
//
// ONE DIFFERENCE: A PROJECT PATH IS SEVERAL SEGMENTS. OneDev nests projects
// arbitrarily, so "acme/team/widget" fills one hole and becomes three path
// segments, each escaped and each checked on its own. That hole is spelled
// differently from the one-segment hole so a template says which it takes,
// and a "/" handed to a one-segment hole is refused rather than split.
type route struct {
	method   string
	template string
	segments []string
	params   []string
	declared string
	ships    bool
}

const (
	// hole is ONE path segment; a "/" in its argument is refused.
	hole = "{}"
	// pathHole is a project PATH: one or more segments, split on "/".
	pathHole = "{...}"
)

var (
	routesMu   sync.Mutex
	routeTable []*route
	enrolled   = map[*route]bool{}
)

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

// THE ROUTING TABLE. Everything this driver asks a OneDev is here.
var (
	routeCurrentUser = newRoute(http.MethodGet, "~api/users/me")

	// The one endpoint that takes a path. Everything after it takes the id it
	// answers with.
	routeProjectID = newRoute(http.MethodGet, "~api/projects/ids/"+pathHole)

	routeListProjects = newRoute(http.MethodGet, "~api/projects", "query", "offset", "count")
	routeCreate       = newRoute(http.MethodPost, "~api/projects")
	routeDelete       = newRoute(http.MethodDelete, "~api/projects/"+hole)
	routeCloneURL     = newRoute(http.MethodGet, "~api/projects/"+hole+"/clone-url")

	// The settings carry the webhooks, and reading them needs "manage
	// project", which is why the same request is also the admin probe.
	routeSetting     = newRoute(http.MethodGet, "~api/projects/"+hole+"/setting")
	routeSaveSetting = newRoute(http.MethodPost, "~api/projects/"+hole+"/setting")

	// Reading it needs "read code".
	routeDefaultBranch = newRoute(http.MethodGet, "~api/repositories/"+hole+"/default-branch")

	// git's own question, asked of git's own endpoint: OneDev answers a
	// receive-pack advertisement only to a credential that may push, and
	// transfers nothing for it. The argument is the project path with ".git".
	routeReceivePack = newRoute(http.MethodGet, pathHole+"/info/refs", "service")
)

type request struct {
	route *route
	args  []string
	query url.Values
}

func (r *route) at(args ...string) request { return request{route: r, args: args} }

func (rq request) with(query url.Values) request {
	rq.query = query
	return rq
}

// escapedSegments is the one place a string becomes part of a path here.
func escapedSegments(segments []string) (string, error) {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		if err := addressesItself(s); err != nil {
			return "", fmt.Errorf("forge/onedev: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself is the gitea driver's rule: a segment must still name the
// place it was put after a resolver removing dot segments has read it, and an
// empty segment names the collection rather than a member of it.
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
		return "", errors.New("forge/onedev: a request with no route names nothing")
	case !registered(r):
		return "", fmt.Errorf("forge/onedev: %s %s was not declared by newRoute, so nothing arms it", r.method, r.template)
	}

	holes := strings.Count(r.template, hole) + strings.Count(r.template, pathHole)
	if len(rq.args) != holes {
		return "", fmt.Errorf("forge/onedev: %s %s takes %d arguments and was given %d", r.method, r.template, holes, len(rq.args))
	}
	segments := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		switch s {
		case hole:
			arg := rq.args[next]
			next++
			if strings.Contains(arg, "/") {
				return "", fmt.Errorf("forge/onedev: %s %s takes one path segment and was given %q, which spans several", r.method, r.template, bounded(arg))
			}
			segments = append(segments, arg)
		case pathHole:
			segments = append(segments, strings.Split(rq.args[next], "/")...)
			next++
		default:
			segments = append(segments, s)
		}
	}
	return escapedSegments(segments)
}

func (rq request) rawQuery() (string, error) {
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/onedev: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
	}
	return rq.query.Encode(), nil
}

func registered(r *route) bool {
	routesMu.Lock()
	defer routesMu.Unlock()
	return enrolled[r]
}

// matches reports whether one request that went out is this route's. A path
// hole takes one or more segments; there is at most one per template, so the
// literals before it must match from the front and those after it from the
// back.
func (r *route) matches(base *url.URL, method, escapedPath string) bool {
	if method != r.method {
		return false
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(escapedPath, prefix) {
		return false
	}
	got := strings.Split(strings.TrimPrefix(escapedPath, prefix), "/")
	at := slices.Index(r.segments, pathHole)
	if at < 0 {
		return len(got) == len(r.segments) && literalsMatch(r.segments, got)
	}
	before, after := r.segments[:at], r.segments[at+1:]
	if len(got) < len(before)+len(after)+1 {
		return false
	}
	return literalsMatch(before, got[:len(before)]) && literalsMatch(after, got[len(got)-len(after):])
}

func literalsMatch(template, got []string) bool {
	for i, s := range template {
		if s == hole {
			continue
		}
		decoded, err := url.PathUnescape(got[i])
		if err != nil || decoded != s {
			return false
		}
	}
	return true
}
