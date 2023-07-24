package service

import (
	"context"
	"time"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Router().AddStatic("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Router().AddStatic("push", srv.pushHandler) // TODO: requires secret + maybe a handshare using project PSK
	srv.stream.Router().AddStatic("fetch", srv.fetchHandler)
	srv.stream.Router().AddStatic("lookup", srv.lookupHandler)
	srv.stream.Router().AddStatic("list", srv.listHandler)
	// a node can suscribe to a regexp pubsub
	// name /tns/updates/<updated key name>
}
