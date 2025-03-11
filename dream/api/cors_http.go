package api

import (
	"net/http"

	"github.com/taubyte/tau/dream/cors"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) corsHttp() {
	srv.rest.LowLevel(&httpIface.LowLevelDefinition{
		Path: "/cors",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			cors.ProxyHandler(w, r)
		},
	})
}
