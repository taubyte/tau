package seer

import (
	"context"
	"errors"
	"time"

	authService "github.com/taubyte/tau/services/auth"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	"github.com/taubyte/tau/utils/maps"
)

func (srv *Service) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {
		rctx, rctx_cancel := context.WithTimeout(ctx.Request().Context(), time.Duration(30)*time.Second)
		client, err := authService.NewGitHubClient(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("invalid Github token")
		}

		ctx.SetVariable("GithubClient", client)
		ctx.SetVariable("GithubClientDone", rctx_cancel)

		logger.Debugf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())

		return nil, nil
	}
	return nil, errors.New("valid Github token required")
}

func (srv *Service) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	done, k := ctx.Variables()["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}

// authorized is what a gateway route that serves something a project owns
// declares: authenticate the caller, then authorize the object the path names.
func (srv *Service) authorized(object func(http.Context) error) http.Handler {
	return authService.RouteAuth(srv.GitHubTokenHTTPAuth, srv.GitHubTokenHTTPAuthCleanup, object)
}

// authorizeProjectVariable refuses a caller who cannot write to the
// repositories the project named in the path is linked to. That is the same
// question auth answers for its own project routes, asked here through the
// same helper so there is one rule and not two.
func (srv *Service) authorizeProjectVariable(ctx http.Context) error {
	if srv.devMode {
		return nil
	}

	projectId, err := maps.String(ctx.Variables(), "projectId")
	if err != nil {
		return err
	}

	return authService.AuthorizeProject(ctx, srv.authClient, projectId)
}
