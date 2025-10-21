package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/specs/common"
)

func (srv *Service) validClients() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/spec/clients",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return common.P2PStreamServices, nil
		},
	})
}
