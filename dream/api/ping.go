package api

import (
	httpIface "github.com/taubyte/http"
)

func (srv *multiverseService) pingHttp() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/ping",
		Handler: func(httpIface.Context) (interface{}, error) {
			return "pong", nil
		},
	})
}
