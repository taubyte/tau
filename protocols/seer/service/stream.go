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

	srv.stream.Router().AddStatic("geo", srv.geo.locationServiceHandler)
	srv.stream.Router().AddStatic("heartbeat", srv.oracle.heartbeatServiceHandler)
	srv.stream.Router().AddStatic("announce", srv.oracle.announceServiceHandler)
}
