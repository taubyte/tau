package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bitbucket.org/taubyte/auth/github"
	"github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/services/http"
	httpAuth "github.com/taubyte/http/auth"
)

func (srv *BillingService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {
		rctx, rctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)
		client, err := github.New(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("Invalid Github token")
		}
		ctx.SetVariable("GithubClient", client)
		ctx.SetVariable("GithubClientDone", rctx_cancel)
		logger.Debug(moody.Object{"message": fmt.Sprintf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())})
		return nil, nil
	}
	return nil, errors.New("Valid Github token required")
}

func (srv *BillingService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	variables := ctx.Variables()
	done, k := variables["GithubClientDone"]
	if k == true && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
