package basic_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	service "github.com/taubyte/tau/pkg/http"
	basic "github.com/taubyte/tau/pkg/http/basic"
	"gotest.tools/v3/assert"
)

// TestRouterRunsTheObjectHalf pins the router's side of a two-halved auth
// block. A route naming the object it authorizes is a declaration, and a
// declaration nothing reads is decoration: unless the router asks the object
// check, every route in every service could name one and still answer anybody
// the token check let through.
func TestRouterRunsTheObjectHalf(t *testing.T) {
	refused := errors.New("not yours")

	t.Run("object refuses", func(t *testing.T) {
		var authenticated, asked, served, released int

		srv, err := basic.New(context.Background())
		assert.NilError(t, err)

		srv.GET(&service.RouteDefinition{
			Path: "/thing/{id}",
			Auth: service.RouteAuthHandler{
				Validator: func(service.Context) (interface{}, error) { authenticated++; return nil, nil },
				Object:    func(service.Context) error { asked++; return refused },
				GC:        func(service.Context) (interface{}, error) { released++; return nil, nil },
			},
			Handler: func(service.Context) (interface{}, error) { served++; return "the thing", nil },
		})

		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, httptest.NewRequest("GET", "http://host/thing/1", nil))

		assert.Equal(t, http.StatusUnauthorized, w.Code, "a refused caller was answered: %s", w.Body.String())
		assert.Equal(t, 1, authenticated)
		assert.Equal(t, 1, asked, "the object check the route declared was never asked")
		assert.Equal(t, 0, served, "the handler ran for a caller the object check refused")
		assert.Equal(t, 1, released, "the refusal never released what the token check built")
	})

	t.Run("object allows", func(t *testing.T) {
		var asked, served int

		srv, err := basic.New(context.Background())
		assert.NilError(t, err)

		srv.GET(&service.RouteDefinition{
			Path: "/thing/{id}",
			Auth: service.RouteAuthHandler{
				Validator: func(service.Context) (interface{}, error) { return nil, nil },
				Object:    func(service.Context) error { asked++; return nil },
			},
			Handler: func(service.Context) (interface{}, error) { served++; return "the thing", nil },
		})

		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, httptest.NewRequest("GET", "http://host/thing/1", nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, asked)
		assert.Equal(t, 1, served, "a caller both halves allowed was refused")
	})

	// A route whose path names nothing owned declares no object check, and the
	// token check alone still decides.
	t.Run("no object declared", func(t *testing.T) {
		var served int

		srv, err := basic.New(context.Background())
		assert.NilError(t, err)

		srv.GET(&service.RouteDefinition{
			Path: "/ping",
			Auth: service.RouteAuthHandler{
				Validator: func(service.Context) (interface{}, error) { return nil, refused },
			},
			Handler: func(service.Context) (interface{}, error) { served++; return "pong", nil },
		})

		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, httptest.NewRequest("GET", "http://host/ping", nil))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, 0, served)
	})
}
