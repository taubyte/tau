package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) pingHttp() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/ping",
		Handler: func(httpIface.Context) (interface{}, error) {
			return "pong", nil
		},
	})
}
