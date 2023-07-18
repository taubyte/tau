package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/odo/protocols/auth/github"

	"github.com/taubyte/go-interfaces/services/http"
	httpAuth "github.com/taubyte/http/auth"
)

func (srv *Service) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {
		rctx, rctx_cancel := context.WithTimeout(ctx.Request().Context(), time.Duration(30)*time.Second)
		client, err := github.New(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("invalid Github token")
		}

		ctx.SetVariable("GithubClient", client)
		ctx.SetVariable("GithubClientDone", rctx_cancel)

		debugMsg := fmt.Sprintf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())
		logger.Debug(map[string]interface{}{"message": debugMsg})

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
