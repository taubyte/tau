package onedev

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A route this test declares, so the plumbing's refusals can be driven. It is
// not shipped, and the sweep knows the difference.
var routeTestOnly = newRoute(http.MethodGet, "~api/test/"+hole+"/x")

func TestAPathIsComposedOnlyFromADeclaredRoute(t *testing.T) {
	_, err := request{}.path()
	require.ErrorContains(t, err, "no route")

	stray := &route{method: http.MethodGet, template: "~api/stray", segments: []string{"~api", "stray"}}
	_, err = stray.at().path()
	require.ErrorContains(t, err, "not declared")

	_, err = routeTestOnly.at().path()
	require.ErrorContains(t, err, "takes 1 arguments")
	_, err = routeTestOnly.at("a/b").path()
	require.ErrorContains(t, err, "spans several", "a one-segment hole is not split")
	_, err = routeTestOnly.at("..").path()
	require.ErrorContains(t, err, "is not a name")
	_, err = routeProjectID.at("acme//pay").path()
	require.ErrorContains(t, err, "empty path segment")

	p, err := routeProjectID.at("acme/team/p#y").path()
	require.NoError(t, err)
	assert.Equal(t, "~api/projects/ids/acme/team/p%23y", p, "each segment of a path hole escaped on its own")

	_, err = routeTestOnly.at("a").with(url.Values{"nope": {"1"}}).rawQuery()
	require.ErrorContains(t, err, "does not declare")
}

func TestBoundedCutsAStrangersString(t *testing.T) {
	long := strings.Repeat("x", maxHostString+10)
	assert.Equal(t, maxHostString+len("…"), len(bounded(long)))
	assert.Equal(t, "short", bounded("short"))
}

func TestMatches(t *testing.T) {
	base := &url.URL{Scheme: "https", Host: "h", Path: "/"}
	assert.True(t, routeProjectID.matches(base, http.MethodGet, "/~api/projects/ids/a/b/c"))
	assert.False(t, routeProjectID.matches(base, http.MethodGet, "/~api/projects/ids"), "a path hole takes at least one segment")
	assert.False(t, routeProjectID.matches(base, http.MethodPost, "/~api/projects/ids/a/b"))
	assert.False(t, routeProjectID.matches(&url.URL{Path: "/other/"}, http.MethodGet, "/~api/projects/ids/a/b"))
	assert.True(t, routeReceivePack.matches(base, http.MethodGet, "/a/b.git/info/refs"))
	assert.False(t, routeReceivePack.matches(base, http.MethodGet, "/a/b.git/info/nope"))
	assert.True(t, routeCloneURL.matches(base, http.MethodGet, "/~api/projects/7/clone-url"))
	assert.False(t, routeCloneURL.matches(base, http.MethodGet, "/~api/projects/7/clone-url/x"))
	assert.False(t, routeCloneURL.matches(base, http.MethodGet, "/~api/projects/7/%zz"))
}

func TestSendRefusesWhatItCannotSend(t *testing.T) {
	d, err := New("onedev.example")
	require.NoError(t, err)
	c := d.(*driver).bind("t")
	ctx := context.Background()

	require.ErrorContains(t, c.send(ctx, routeCreate.at(), func() {}, nil), "encoding")
	require.Error(t, c.send(ctx, routeTestOnly.at(), nil, nil))
	require.Error(t, c.send(ctx, routeTestOnly.at("a").with(url.Values{"nope": {"1"}}), nil, nil))
	require.ErrorContains(t, c.send(nilContext(), routeCurrentUser.at(), nil, nil), "building")
}

// nilContext is what NewRequestWithContext refuses, which is the one way left
// to make building a request fail.
func nilContext() context.Context {
	var ctx context.Context
	return ctx
}
