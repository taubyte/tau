package basic

import (
	"net/http"

	"github.com/gorilla/mux"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/context"
	"github.com/taubyte/tau/pkg/http/request"
)

func (s *Service) GET(def *service.RouteDefinition) {
	s.buildRouteFromDef(def).Methods("GET")
}

func (s *Service) PUT(def *service.RouteDefinition) {
	s.buildRouteFromDef(def).Methods("PUT")
}

func (s *Service) POST(def *service.RouteDefinition) {
	s.buildRouteFromDef(def).Methods("POST")
}

func (s *Service) DELETE(def *service.RouteDefinition) {
	s.buildRouteFromDef(def).Methods("DELETE")
}

func (s *Service) PATCH(def *service.RouteDefinition) {
	s.buildRouteFromDef(def).Methods("PATCH")
}

func (s *Service) ALL(def *service.RouteDefinition) {
	s.buildRouteFromDef(def)
}

func (s *Service) Raw(def *service.RawRouteDefinition) *mux.Route {
	var route *mux.Route
	if len(def.PathPrefix) > 0 {
		route = s.Router.PathPrefix(def.PathPrefix)
	} else {
		route = s.Router.Path(def.Path)
	}

	route.HandlerFunc(func(w http.ResponseWriter, h *http.Request) {
		logger.Debugf("[RAW] %s", h.RequestURI)
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

func (s *Service) LowLevel(def *service.LowLevelDefinition) *mux.Route {
	if len(def.PathPrefix) > 0 {
		return s.Router.PathPrefix(def.Path).HandlerFunc(def.Handler)
	}

	return s.Router.Path(def.Path).HandlerFunc(def.Handler)
}
