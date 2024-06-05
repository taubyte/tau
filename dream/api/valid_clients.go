package api

import (
	httpIface "github.com/taubyte/http"
	"github.com/taubyte/tau/pkg/specs/common"
)

func (srv *multiverseService) validClients() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/spec/clients",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return common.P2PStreamServices, nil
		},
	})
}
