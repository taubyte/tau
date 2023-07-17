package service

import (
	"context"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/go-interfaces/p2p/streams"
)

func (srv *BillingService) setupStreamRoutes() {
	srv.stream.Router().AddStatic("ping", func(context.Context, streams.Connection, streams.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Router().AddStatic("customers", srv.customers.serviceHandler)
	srv.stream.Router().AddStatic("counters", srv.countersHandler)
}
