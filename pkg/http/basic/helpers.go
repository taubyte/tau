package basic

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	service "github.com/taubyte/tau/pkg/http"
	auth "github.com/taubyte/tau/pkg/http/auth"
	"github.com/taubyte/tau/pkg/http/context"
	"github.com/taubyte/tau/pkg/http/request"
	"github.com/taubyte/tau/pkg/platformerr"
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
		if platformerr.CodeOf(err) == platformerr.Rejected {
			// anyone can send these: a warning, not an error
			logger.Warnf("rejected %s: %v", req.HttpRequest.URL.Path, err)
		} else {
			logger.Error(fmt.Errorf("calling %s failed with %v", req.HttpRequest.URL, err))
		}
	}

	// never the variables: they hold the caller's token
	logger.Debugf("%s done", req.HttpRequest.URL.Path)
}

func (s *Service) buildRouteFromDef(def *service.RouteDefinition) *mux.Route {
	route := s.Router.HandleFunc(def.Path, func(w http.ResponseWriter, h *http.Request) {
		logger.Debugf("[GET] %s", h.URL.Path)
		options := make([]context.Option, 0)
		if def.RawResponse {
			options = append(options, context.RawResponse())
		}
		s.handleRequest(&request.Request{ResponseWriter: w, HttpRequest: h}, &def.Vars, def.Scope, def.Auth.Validator, def.Handler, def.Auth.GC, options...)
	})

	applyHostMatch(route, def.Hosts)

	return route
}
