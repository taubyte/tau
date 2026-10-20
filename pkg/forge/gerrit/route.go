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

// THE PATH OF A REQUEST IS A TEMPLATE FROM A CLOSED SET, never a string a call
// site pasted together: every segment reaches a path through segment(), which
// escapes it, and every query parameter is declared by the route that may
// carry it. This is the gitea driver's rule; what differs is the hole. A
// Gerrit project name is a PATH that fills ONE segment, so "acme/pay" is
// escaped to acme%2Fpay — which is exactly what the API expects, and exactly
// why a "/" inside a hole is not refused here the way gitea refuses it.
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

// segment IS THE ONE PLACE A STRING BECOMES PART OF A PATH IN THIS PACKAGE.
//
// It refuses what escaping does not fix: an empty segment addresses the
// collection, and a value a resolver would move — "..", ".", "acme/../x" —
// addresses something the caller never named. url.PathEscape leaves dots
// alone, and Gerrit decodes the segment back into exactly the string checked
// here, so the raw spelling is the one that matters.
func segment(s string) (string, error) {
	if s == "" {
		return "", errors.New("forge/gerrit: an empty path segment addresses the collection the request is under")
	}
	if path.Clean("/"+s) != "/"+s {
		return "", fmt.Errorf("forge/gerrit: %q is not a name: a resolver removing dot segments does not leave it where this driver put it", bounded(s))
	}
	return url.PathEscape(s), nil
}

// bounded cuts a string SOMEBODY ELSE CHOSE to a length an error may carry.
func bounded(s string) string {
	if len(s) <= maxHostString {
		return s
	}
	return s[:maxHostString] + "…"
}

// path is the escaped path this request names, or the reason it is not one.
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
