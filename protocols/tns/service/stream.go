package service

import (
	"context"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/go-interfaces/p2p/streams"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Router().AddStatic("ping", func(context.Context, streams.Connection, streams.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Router().AddStatic("push", srv.pushHandler) // TODO: requires secret + maybe a handshare using project PSK
	srv.stream.Router().AddStatic("fetch", srv.fetchHandler)
	srv.stream.Router().AddStatic("lookup", srv.lookupHandler)
	srv.stream.Router().AddStatic("list", srv.listHandler)
	// a node can suscribe to a regexp pubsub
	// name /tns/updates/<updated key name>
}
