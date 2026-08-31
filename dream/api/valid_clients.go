package api

import (
	"github.com/taubyte/tau/legacy/specs/common"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) validClients() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/spec/clients",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return common.P2PStreamServices, nil
		},
	})
}
