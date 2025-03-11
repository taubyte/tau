package auth

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/auth/github"
	"github.com/taubyte/tau/services/auth/repositories"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/utils/maps"
)

func getGithubClientFromContext(ctx http.Context) (*github.Client, error) {
	ctxVars := ctx.Variables()
	v, k := ctxVars["GithubClient"]
	if !k {
		return nil, errors.New("no Github Client found")
	}

	return v.(*github.Client), nil
}

func extractProjectVariables(ctx http.Context) (configID, codeID, projectName string, err error) {
	ctxVars := ctx.Variables()
	config_repo, err := maps.InterfaceToStringKeys(ctxVars["config"])
	if err != nil {
		return
	}
	if configID, err = maps.String(config_repo, "id"); err != nil {
		return
	}

	code_repo, err := maps.InterfaceToStringKeys(ctxVars["code"])
	if err != nil {
		return
	}
	if codeID, err = maps.String(code_repo, "id"); err != nil {
		return
	}

	// Extract project name from path
	projectVar := ctxVars["project"]
	switch v := projectVar.(type) {
	case string:
		projectName = v
	default:
		err = errors.New("invalid value for project name")
	}

	return
}

func (srv *AuthService) newGitHubProjectHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	configID, codeID, projectName, err := extractProjectVariables(ctx)
	if err != nil {
		return nil, err
	}

	projectID := protocolCommon.GetNewProjectID(projectName, time.Now().Unix(), rand.Intn(1000000000))
	return srv.newGitHubProject(ctx.Request().Context(), client, projectID, projectName, configID, codeID)
}

func (srv *AuthService) importGitHubProjectHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	configID, codeID, projectName, err := extractProjectVariables(ctx)
	if err != nil {
		return nil, err
	}

	// extract projectID from call
	ctxVars := ctx.Variables()
	projectID, err := maps.String(ctxVars, "project-id")
	if err != nil {
		return nil, err
	}

	return srv.newGitHubProject(ctx.Request().Context(), client, projectID, projectName, configID, codeID)
}

func (srv *AuthService) registerGitHubUserRepositoryHTTPHandler(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	provider, err := maps.String(ctxVars, "provider")
	if err != nil {
		return nil, err
	}
	if provider != "github" {
		return nil, fmt.Errorf("provider `%s` is not supported", provider)
	}
	repoId, err := maps.String(ctxVars, "id")
	if err != nil {
		return nil, fmt.Errorf("parsing github repository ID failed with %w", err)
	}
	response, err := srv.registerGitHubRepository(ctx.Request().Context(), client, repoId)
	return response, err
}

func (srv *AuthService) getGitHubUserRepositoryHTTPHandler(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	provider, err := maps.String(ctxVars, "provider")
	if err != nil {
		return nil, err
	}

	repoId, err := maps.String(ctxVars, "id")
	if err != nil {
		return nil, fmt.Errorf("parsing github repository ID failed with %w", err)
	}

	requestCtx := ctx.Request().Context()
	if !repositories.ExistOn(requestCtx, srv.db, provider, repoId) {
		return nil, fmt.Errorf("repository %s not found", repoId)
	}

	repo, err := repositories.Fetch(requestCtx, srv.db, repoId)
	if err != nil {
		return nil, fmt.Errorf("fetching repository %s failed with %w", repoId, err)
	}

	hks := make([]string, 0)
	for _, h := range repo.Hooks(requestCtx) {
		hks = append(hks, h.ProviderID())
	}

	return map[string]interface{}{"hooks": hks}, err
}

func (srv *AuthService) unregisterGitHubUserRepositoryHTTPHandler(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	provider, err := maps.String(ctxVars, "provider")
	if err != nil {
		return nil, err
	}
	if provider != "github" {
		return nil, fmt.Errorf("provider `%s` is not supported", provider)
	}

	repoId, err := maps.String(ctxVars, "id")
	if err != nil {
		return nil, fmt.Errorf("parsing github repository ID failed with %w", err)
	}

	response, err := srv.unregisterGitHubRepository(ctx.Request().Context(), client, repoId)

	return response, err
}

func (srv *AuthService) getGitHubUserProjectsHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	response, err := srv.getGitHubUserProjects(ctx.Request().Context(), client)
	return response, err
}

func (srv *AuthService) deleteGitHubProjectHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	ctxVars := ctx.Variables()
	response, err := srv.deleteGitHubUserProject(ctx.Request().Context(), client, ctxVars["id"].(string))
	return response, err
}

func (srv *AuthService) getGitHubUserRepositoriesHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	response, err := srv.getGitHubUserRepositories(ctx.Request().Context(), client)
	return response, err
}

func (srv *AuthService) getGitHubProjectInfoHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	ctxVars := ctx.Variables()
	switch ctxVars["id"].(type) {
	case string:
		response, err := srv.getGitHubProjectInfo(ctx.Request().Context(), client, ctxVars["id"].(string))
		return response, err
	}
	return nil, errors.New("invalid value for project id")
}

func (srv *AuthService) getGitHubUserHTTPHandler(ctx http.Context) (interface{}, error) {
	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	response, err := srv.getGitHubUser(client)
	return response, err
}

func (srv *AuthService) setupGitHubHTTPRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "auth.tau." + srv.hostUrl
	}

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/project/new/{project}",
		Vars: http.Variables{
			Required: []string{"project", "config", "code"},
		},
		Scope: []string{"projects/new"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.newGitHubProjectHTTPHandler,
	})

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/project/import/{project}",
		Vars: http.Variables{
			Required: []string{"project", "config", "code", "project-id"},
		},
		Scope: []string{"projects/import"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.importGitHubProjectHTTPHandler,
	})

	srv.http.PUT(&http.RouteDefinition{
		Host: host,
		Path: "/repository/{provider}/{id}",
		Vars: http.Variables{
			Required: []string{"provider", "id"},
		},
		Scope: []string{"repositories/write"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.registerGitHubUserRepositoryHTTPHandler,
	})

	srv.http.DELETE(&http.RouteDefinition{
		Host: host,
		Path: "/repository/{provider}/{id}",
		Vars: http.Variables{
			Required: []string{"provider", "id"},
		},
		Scope: []string{"repositories/write"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.unregisterGitHubUserRepositoryHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/repository/{provider}/{id}",
		Vars: http.Variables{
			Required: []string{"provider", "id"},
		},
		Scope: []string{"repositories/read"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getGitHubUserRepositoryHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host:  host,
		Path:  "/repositories",
		Scope: []string{"repositories/read"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getGitHubUserRepositoriesHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host:  host,
		Path:  "/projects",
		Scope: []string{"projects/read"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getGitHubUserProjectsHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/projects/{id}",
		Vars: http.Variables{
			Required: []string{"id"},
		},
		Scope: []string{"projects/read"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getGitHubProjectInfoHTTPHandler,
	})

	srv.http.DELETE(&http.RouteDefinition{
		Host: host,
		Path: "/projects/{id}",
		Vars: http.Variables{
			Required: []string{"id"},
		},
		Scope: []string{"projects/write"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.deleteGitHubProjectHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host:  host,
		Path:  "/me",
		Scope: []string{"user/self"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getGitHubUserHTTPHandler,
	})

}
