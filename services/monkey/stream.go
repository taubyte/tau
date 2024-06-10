package monkey

import (
	"context"
	"time"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})
	srv.stream.Define("job", srv.ServiceHandler)
}
