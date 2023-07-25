package seer

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

	srv.stream.Router().AddStatic("geo", srv.geo.locationServiceHandler)
	srv.stream.Router().AddStatic("heartbeat", srv.oracle.heartbeatServiceHandler)
	srv.stream.Router().AddStatic("announce", srv.oracle.announceServiceHandler)
}
