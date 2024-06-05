package api

import (
	"net/http"

	httpIface "github.com/taubyte/http"
	"github.com/taubyte/tau/dream/cors"
)

func (srv *multiverseService) corsHttp() {
	srv.rest.LowLevel(&httpIface.LowLevelDefinition{
		Path: "/cors",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			cors.ProxyHandler(w, r)
		},
	})
}
