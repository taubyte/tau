package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
)

func (srv *AuthService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {

		rctx, rctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)

		client, err := srv.newGitHubClient(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("invalid Github token")
		}

		// Reject logins whose github user isn't linked to any tau Account.
		// Linked accounts get stashed on the http context for downstream use.
		if srv.accountsClient != nil {
			gh := client.Me()
			if gh == nil || gh.ID == nil {
				rctx_cancel()
				return nil, errors.New("github user identity unavailable")
			}
			externalID := fmt.Sprintf("%d", *gh.ID)
			vresp, verr := srv.accountsClient.Verify(rctx, "github", externalID)
			if verr != nil {
				rctx_cancel()
				return nil, fmt.Errorf("accounts verify failed: %w", verr)
			}
			if !vresp.Linked {
				rctx_cancel()
				if srv.accountsURL != "" {
					return nil, fmt.Errorf("no tau account linked to this github identity — sign up at %s", srv.accountsURL)
				}
				return nil, errors.New("no tau account linked to this github identity")
			}
			ctx.SetVariable("LinkedAccounts", vresp.Accounts)
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
