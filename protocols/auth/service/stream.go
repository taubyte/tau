package service

import (
	"context"
	"time"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
)

func (srv *AuthService) setupStreamRoutes() {
	srv.stream.Router().AddStatic("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Router().AddStatic("acme", srv.acmeServiceHandler)
	srv.stream.Router().AddStatic("hooks", srv.apiHookServiceHandler)
	srv.stream.Router().AddStatic("repositories", srv.apiGitRepositoryServiceHandler)
	srv.stream.Router().AddStatic("projects", srv.apiProjectsServiceHandler)
}
