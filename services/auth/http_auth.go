package auth

import (
	"context"
	"errors"
	"time"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
)

// GitHubTokenHTTPAuth validates the caller's provider token and then answers
// whether that identity may use this cloud's API at all. Every authenticated
// route runs it, including ones that only read — listing projects, deleting one
// and issuing a domain token all hang off this single gate, so the identity
// question cannot be answered per-route without leaving some of them open.
//
// How the second question is answered differs per build: see identity.go and
// identity_ee.go.
func (srv *AuthService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth == nil || (auth.Type != "oauth" && auth.Type != "github") {
		return nil, errors.New("valid Github token required")
	}

	rctx, rctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)

	client, err := srv.newGitHubClient(rctx, auth.Token)
	if err != nil {
		rctx_cancel()
		return nil, errors.New("invalid Github token")
	}

	if err := srv.authorizeIdentity(rctx, ctx, client); err != nil {
		rctx_cancel()
		return nil, err
	}

	ctx.SetVariable("GithubClient", client)
	ctx.SetVariable("GithubClientDone", rctx_cancel)

	logger.Debugf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())

	return nil, nil
}

func (srv *AuthService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	done, k := ctxVars["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
