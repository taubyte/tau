package basic

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	service "github.com/taubyte/tau/pkg/http"
	auth "github.com/taubyte/tau/pkg/http/auth"
	"github.com/taubyte/tau/pkg/http/context"
	"github.com/taubyte/tau/pkg/http/request"
)

func (s *Service) handleRequest(req *request.Request, vars *service.Variables, scope []string, authHandler service.Handler, handler service.Handler, cleanupHandler service.Handler, options ...context.Option) {
	ctx, err := context.New(req, vars, options...)
	if err != nil {
		logger.Error(err)
		return
	}

	if err = ctx.HandleAuth(auth.Scope(scope, authHandler)); err != nil {
		logger.Error(err)
		return
	}

	defer func() {
		if err := ctx.HandleCleanup(cleanupHandler); err != nil {
			logger.Errorf("cleanup failed with: %s", err)
		}
	}()

	if err = ctx.HandleWith(handler); err != nil {
		logger.Error(fmt.Errorf("calling %s failed with %v", req.HttpRequest.URL, err))
	}

	logger.Debugf("%s | %v", string(req.HttpRequest.RequestURI), ctx.Variables())
}

func (s *Service) buildRouteFromDef(def *service.RouteDefinition) *mux.Route {
	route := s.Router.HandleFunc(def.Path, func(w http.ResponseWriter, h *http.Request) {
		logger.Debugf("[GET] %s", h.RequestURI)
		options := make([]context.Option, 0)
		if def.RawResponse {
			options = append(options, context.RawResponse())
		}
		s.handleRequest(&request.Request{ResponseWriter: w, HttpRequest: h}, &def.Vars, def.Scope, def.Auth.Validator, def.Handler, def.Auth.GC, options...)
	})

	if len(def.Host) > 0 {
		route.Host(def.Host)
	}

	return route
}
