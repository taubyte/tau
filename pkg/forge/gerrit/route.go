package gerrit

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// A request path is a template from a closed set, the gitea driver's rule. A
// project name fills one segment, so "acme/pay" escapes to acme%2Fpay, as the
// API expects, and a "/" in a hole is not refused.
type route struct {
	method   string
	template string
	segments []string
	params   []string
}

const hole = "{}"

func newRoute(method, template string, params ...string) *route {
	return &route{method: method, template: template, segments: strings.Split(template, "/"), params: params}
}

// THE ROUTING TABLE. Everything this driver asks a Gerrit is here. Paths are
// under the client's prefix: /a/ when authenticated, / when anonymous.
var (
	routeSelf = newRoute(http.MethodGet, "accounts/self")

	// n is the page size and S the offset; Gerrit has no page counter.
	routeListProjects = newRoute(http.MethodGet, "projects/", "n", "S")

	routeProject       = newRoute(http.MethodGet, "projects/"+hole)
	routeProjectHEAD   = newRoute(http.MethodGet, "projects/"+hole+"/HEAD")
	routeProjectAccess = newRoute(http.MethodGet, "projects/"+hole+"/access")

	// routeBranches lists a project's branches with the revision each points
	// at — the heads a reconnecting event stream compares against.
	routeBranches = newRoute(http.MethodGet, "projects/"+hole+"/branches/")

	routeCreateProject   = newRoute(http.MethodPut, "projects/"+hole)
	routeRestrictProject = newRoute(http.MethodPost, "projects/"+hole+"/access")

	// The delete-project plugin's endpoint, bundled with every Gerrit release
	// image. A Gerrit without the plugin answers it with an error, which is
	// what it is.
	routeDeleteProject = newRoute(http.MethodDelete, "projects/"+hole)

	// The DEPLOYMENT's credential, on a client no session can reach.
	routeGroup       = newRoute(http.MethodGet, "groups/"+hole)
	routeGroupMember = newRoute(http.MethodGet, "groups/"+hole+"/members/"+hole)
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

// segment is the one place a string becomes part of a path. It refuses an empty
// segment, which addresses the collection, and one a resolver would move; Gerrit
// decodes the segment back to exactly the string checked here.
func segment(s string) (string, error) {
	if s == "" {
		return "", errors.New("forge/gerrit: an empty path segment addresses the collection the request is under")
	}
	if path.Clean("/"+s) != "/"+s {
		return "", fmt.Errorf("forge/gerrit: %q is not a name: a resolver removing dot segments does not leave it where this driver put it", bounded(s))
	}
	return url.PathEscape(s), nil
}

func bounded(s string) string {
	if len(s) <= maxHostString {
		return s
	}
	return s[:maxHostString] + "…"
}

func (rq request) path() (string, error) {
	r := rq.route
	out := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			out = append(out, s)
			continue
		}
		if next == len(rq.args) {
			return "", fmt.Errorf("forge/gerrit: %s %s takes %d arguments and was given %d", r.method, r.template, strings.Count(r.template, hole), len(rq.args))
		}
		seg, err := segment(rq.args[next])
		if err != nil {
			return "", err
		}
		out = append(out, seg)
		next++
	}
	if next != len(rq.args) {
		return "", fmt.Errorf("forge/gerrit: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return strings.Join(out, "/"), nil
}

func (rq request) rawQuery() (string, error) {
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/gerrit: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
	}
	return rq.query.Encode(), nil
}
