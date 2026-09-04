package auth

import (
	"errors"
	"testing"

	http "github.com/taubyte/tau/pkg/http"
	"gotest.tools/v3/assert"
)

// TestAuthorizeRepositoriesEdges pins the answers that are not about
// permissions at all: nothing to ask with, and a stored spelling that is not a
// key. Both are refusals, because a rule that cannot be evaluated has not been
// satisfied.
func TestAuthorizeRepositoriesEdges(t *testing.T) {
	caller := &authzClient{id: 1, login: "caller", writable: map[string]bool{"github.com/caller/code": true}}

	assert.Error(t, AuthorizeRepositories(nil, "github.com/caller/code"), ErrProjectNotFound.Error())
	assert.Error(t, AuthorizeRepositories(caller), ErrProjectNotFound.Error())
	assert.Error(t, AuthorizeRepositories(caller, "not a repository key"), ErrProjectNotFound.Error())
	assert.NilError(t, AuthorizeRepositories(caller, "not a repository key", "github.com/caller/code"))
}

// routeAuthContext is the little of an http.Context that an auth handler reads.
type routeAuthContext struct {
	http.Context
	variables map[string]interface{}
}

func (c *routeAuthContext) Variables() map[string]interface{}       { return c.variables }
func (c *routeAuthContext) SetVariable(key string, val interface{}) { c.variables[key] = val }

// TestRouteAuthOrdering pins the two halves and their order: the object is never
// asked about before the caller is known, and a refusal by the object check
// releases what the token check built — that handler never reaches the route's
// GC, so this is the only place the release can happen.
func TestRouteAuthOrdering(t *testing.T) {
	ctx := &routeAuthContext{variables: map[string]interface{}{}}

	var authenticated, asked, released int
	authenticate := func(http.Context) (interface{}, error) { authenticated++; return nil, nil }
	cleanup := func(http.Context) (interface{}, error) { released++; return nil, nil }

	refused := errors.New("not yours")

	t.Run("object refuses", func(t *testing.T) {
		_, err := RouteAuth(authenticate, cleanup, func(http.Context) error { asked++; return refused })(ctx)
		assert.Error(t, err, refused.Error())
		assert.Equal(t, 1, authenticated)
		assert.Equal(t, 1, asked)
		assert.Equal(t, 1, released)
	})

	t.Run("both pass", func(t *testing.T) {
		_, err := RouteAuth(authenticate, cleanup, func(http.Context) error { asked++; return nil })(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 2, authenticated)
		assert.Equal(t, 2, asked)
		assert.Equal(t, 1, released, "a request that was allowed released the client early")
	})

	t.Run("token refuses first", func(t *testing.T) {
		invalid := errors.New("valid Github token required")
		_, err := RouteAuth(
			func(http.Context) (interface{}, error) { return nil, invalid },
			cleanup,
			func(http.Context) error { asked++; return nil },
		)(ctx)
		assert.Error(t, err, invalid.Error())
		assert.Equal(t, 2, asked, "the object was asked about before the caller was known")
	})
}
