package azure

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// A request path is a template from a closed set: every segment goes through
// escapedSegments, which escapes it and refuses one that would not address
// itself.
type route struct {
	method   string
	template string
	segments []string
	// version is the api-version this route is spoken at. Azure DevOps refuses
	// a request without one, and a preview API refuses the stable spelling.
	version string
	params  []string
}

const hole = "{}"

func newRoute(method, template, version string, params ...string) *route {
	return &route{
		method:   method,
		template: template,
		segments: strings.Split(template, "/"),
		version:  version,
		params:   params,
	}
}

const (
	stable = "7.1"
	// The security evaluation API has only ever been a preview.
	preview = "7.1-preview.1"
)

var (
	routeConnectionData = newRoute(http.MethodGet, hole+"/_apis/connectionData", preview)

	routeListRepositories = newRoute(http.MethodGet, hole+"/_apis/git/repositories", stable)
	routeRepository       = newRoute(http.MethodGet, hole+"/"+hole+"/_apis/git/repositories/"+hole, stable)
	routeProject          = newRoute(http.MethodGet, hole+"/_apis/projects/"+hole, stable)
	routeCreate           = newRoute(http.MethodPost, hole+"/"+hole+"/_apis/git/repositories", stable)
	// Azure DevOps deletes a repository by its id only.
	routeDelete = newRoute(http.MethodDelete, hole+"/"+hole+"/_apis/git/repositories/"+hole, stable)

	routeEvaluate = newRoute(http.MethodPost, hole+"/_apis/security/permissionevaluationbatch", preview)
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

// escapedSegments is the one place a string becomes part of a path.
func escapedSegments(segments []string) (string, error) {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		if err := addressesItself(s); err != nil {
			return "", fmt.Errorf("forge/azure: %w", err)
		}
		escaped[i] = url.PathEscape(s)
	}
	return strings.Join(escaped, "/"), nil
}

// addressesItself refuses a segment a resolver would not leave where it was
// put: empty, ".", "..", or anything holding a "/" that cleans to elsewhere.
// Escaping does not give this — url.PathEscape("..") is "..".
func addressesItself(segment string) error {
	if segment == "" {
		return errors.New("an empty path segment addresses the collection the request is under")
	}
	if path.Clean("/"+segment) != "/"+segment {
		return fmt.Errorf("%q is not a name: a resolver removing dot segments does not leave it where this driver put it", bounded(segment))
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
	segments := make([]string, 0, len(r.segments))
	next := 0
	for _, s := range r.segments {
		if s != hole {
			segments = append(segments, s)
			continue
		}
		if next == len(rq.args) {
			return "", fmt.Errorf("forge/azure: %s %s takes more arguments than it was given", r.method, r.template)
		}
		segments = append(segments, rq.args[next])
		next++
	}
	if next != len(rq.args) {
		return "", fmt.Errorf("forge/azure: %s %s takes %d arguments and was given %d", r.method, r.template, next, len(rq.args))
	}
	return escapedSegments(segments)
}

func (rq request) rawQuery() (string, error) {
	q := url.Values{}
	for name, v := range rq.query {
		if !slices.Contains(rq.route.params, name) {
			return "", fmt.Errorf("forge/azure: %s %s does not declare the query parameter %q", rq.route.method, rq.route.template, name)
		}
		q[name] = v
	}
	q.Set("api-version", rq.route.version)
	return q.Encode(), nil
}
