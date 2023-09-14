package seer

import (
	"context"
	"time"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Define("geo", srv.geo.locationServiceHandler)
	srv.stream.Define("heartbeat", srv.oracle.heartbeatServiceHandler)
	srv.stream.Define("announce", srv.oracle.announceServiceHandler)
}
