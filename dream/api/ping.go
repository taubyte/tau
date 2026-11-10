package api

import (
	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) pingHttp() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/ping",
		Handler: func(httpIface.Context) (interface{}, error) {
			return "pong", nil
		},
	})
}

func (srv *Service) versionHttp() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/version",
		Handler: func(httpIface.Context) (interface{}, error) {
			return dream.BuildVersion(), nil
		},
	})
}
