package service

import (
	"context"
	"errors"
	"time"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	"github.com/taubyte/tau/services/auth/github"
)

func (srv *PatrickService) setupGithubRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "patrick.tau." + srv.hostUrl
	}

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/github/{hook}",
		Vars: http.Variables{
			Required: []string{"hook", "X-Hub-Signature", "X-Hub-Signature-256", "X-GitHub-Hook-ID"},
		},
		Scope: []string{"hook/push"},
		Auth: http.RouteAuthHandler{
			Validator: srv.githubCheckHookAndExtractSecret,
		},
		Handler: srv.githubHookHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/ping",
		Handler: func(ctx http.Context) (interface{}, error) {
			return map[string]string{"ping": "pong"}, nil
		},
	})
}

func (srv *PatrickService) setupJobRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "patrick.tau." + srv.hostUrl
	}

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/jobs/{projectId}",
		Vars: http.Variables{
			Required: []string{"projectId"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.projectAllJobHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/job/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.projectJobHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/download/{jobId}/{resourceId}",
		Vars: http.Variables{
			Required: []string{"jobId", "resourceId"},
		},
		Handler:     srv.downloadAsset,
		RawResponse: true,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/logs/{cid}",
		Vars: http.Variables{
			Required: []string{"cid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler:     srv.cidHandler,
		RawResponse: true,
	})

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/cancel/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.cancelJob,
	})

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/retry/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.retryJob,
	})

}

func (srv *PatrickService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
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
		logger.Debugf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())
		return nil, nil
	}
	return nil, errors.New("valid Github token required")
}

func (srv *PatrickService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	done, k := ctx.Variables()["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
