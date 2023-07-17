package service

import (
	"context"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/go-interfaces/p2p/streams"
)

func (srv *AuthService) setupStreamRoutes() {
	srv.stream.Router().AddStatic("ping", func(context.Context, streams.Connection, streams.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})

	srv.stream.Router().AddStatic("acme", srv.acmeServiceHandler)
	srv.stream.Router().AddStatic("hooks", srv.apiHookServiceHandler)
	srv.stream.Router().AddStatic("repositories", srv.apiGitRepositoryServiceHandler)
	srv.stream.Router().AddStatic("projects", srv.apiProjectsServiceHandler)
}
