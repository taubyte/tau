package auth

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/tau/services/auth/github"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
)

func (srv *AuthService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {

		rctx, rctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)

		client, err := github.New(rctx, auth.Token)
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

func (srv *AuthService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	done, k := ctxVars["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
