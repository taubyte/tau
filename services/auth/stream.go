package auth

import (
	"context"
	"time"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func (srv *AuthService) setupStreamRoutes() {
	srv.stream.Define("ping", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return cr.Response{"time": int(time.Now().Unix())}, nil
		}
	})
	srv.stream.Define("stats", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return srv.statsServiceHandler(ctx, conn, body)
		}
	})
	srv.stream.Define("acme", srv.acmeServiceHandler)
	srv.stream.Define("hooks", srv.apiHookServiceHandler)
	srv.stream.Define("repositories", srv.apiGitRepositoryServiceHandler)
	srv.stream.Define("projects", srv.apiProjectsServiceHandler)
}
