package bitbucketserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// A request path is a template from a closed set, for pkg/forge/bitbucket's
// reasons.
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

const (
	api  = "rest/api/1.0/"
	keys = "rest/keys/1.0/"
	repo = "projects/" + hole + "/repos/" + hole
)

// The routing table. A collection pages with "start" and "limit".
var (
	// whoami answers the authenticated user's slug as plain text; it is the
	// one question REST 1.0 has no resource for.
	routeWhoami = newRoute(http.MethodGet, "plugins/servlet/applinks/whoami")
	routeUser   = newRoute(http.MethodGet, api+"users/"+hole)

	routeRepositories = newRoute(http.MethodGet, api+"repos", "name", "permission", "start", "limit")

	routeRepository       = newRoute(http.MethodGet, api+repo)
	routeDefaultBranch    = newRoute(http.MethodGet, api+repo+"/branches/default")
	routeCreateRepository = newRoute(http.MethodPost, api+"projects/"+hole+"/repos")
	routeDeleteRepository = newRoute(http.MethodDelete, api+repo)

	routeInstallDeployKey = newRoute(http.MethodPost, keys+repo+"/ssh")
	routeListDeployKeys   = newRoute(http.MethodGet, keys+repo+"/ssh", "start", "limit")
	routeRemoveDeployKey  = newRoute(http.MethodDelete, keys+repo+"/ssh/"+hole)

	routeRegisterPushHook = newRoute(http.MethodPost, api+repo+"/webhooks")
	routeListPushHooks    = newRoute(http.MethodGet, api+repo+"/webhooks", "start", "limit")
	routeRemovePushHook   = newRoute(http.MethodDelete, api+repo+"/webhooks/"+hole)
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

func escapedSegments(segments []string) (string, error) {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		if err := addressesItself(s); err != nil {
			return "", fmt.Errorf("forge/bitbucketserver: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself refuses a segment that, escaped perfectly, still names
// something else once dot segments are removed.
func addressesItself(segment string) error {
	if segment == "" {
		return errors.New("an empty path segment addresses the collection the request is under")
	}
	if path.Clean("/"+segment) != "/"+segment {
		return fmt.Errorf("%q is not a name: a resolver removing dot segments would not leave it where this driver put it", bounded(segment))
	}
	return nil
}

func bounded(s string) string {
	if len(s) <= maxHostString {
		return s
	}
	return s[:maxHostString] + "…"
}

func (rq request) path() (string, error) {
	r := rq.route
	segments := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			segments = append(segments, s)
			continue
		}
		if next == len(rq.args) {
			return "", fmt.Errorf("forge/bitbucketserver: %s %s takes %d arguments and was given %d", r.method, r.template, strings.Count(r.template, hole), len(rq.args))
		}
		segments = append(segments, rq.args[next])
		next++
	}
	if next != len(rq.args) {
		return "", fmt.Errorf("forge/bitbucketserver: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return escapedSegments(segments)
}

// rawQuery is the encoded query, refusing a parameter the route did not
// declare.
func (rq request) rawQuery() (string, error) {
	for name := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/bitbucketserver: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
	}
	return rq.query.Encode(), nil
}
