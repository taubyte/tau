package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
)

type UniverseInfo struct {
	Id string `json:"id"`
}

func (srv *Service) idHttp() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/id/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe"},
		},
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			universeName, err := ctx.GetStringVariable("universe")
			if err != nil {
				return nil, err
			}

			u, err := srv.Universe(universeName)
			if err != nil {
				return nil, err
			}

			return UniverseInfo{Id: u.Id()}, nil
		},
	})
}
