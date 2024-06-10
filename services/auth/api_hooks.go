package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/utils/maps"
)

/******* HOOKS ********/
func (srv *AuthService) getRepositoryHookByID(ctx context.Context, hook_id string) (cr.Response, error) {
	hook, err := hooks.Fetch(ctx, srv.db, hook_id)
	if err != nil {
		return nil, err
	}

	return cr.Response(hook.Serialize()), nil
}

func (srv *AuthService) listHooks(ctx context.Context) (cr.Response, error) {
	hids, err := srv.db.List(ctx, "/hooks/")
	if err != nil {
		return nil, err
	}

	ids := extractIdFromKey(hids, "/", 2)

	return cr.Response{"hooks": ids}, nil
}

func (srv *AuthService) apiHookServiceHandler(ctx context.Context, st streams.Connection, body command.Body) (cr.Response, error) {
	// params:
	//  TODO: add encrption key to service library
	//  action: get/set
	//  fqdn: domain name
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "get":
		hook_id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		return srv.getRepositoryHookByID(ctx, hook_id)
	case "list":
		return srv.listHooks(ctx)
	default:
		return nil, errors.New("Hook action `" + action + "` not reconized.")
	}
}

/******* REPOS ********/
func (srv *AuthService) getGithubRepositoryByID(ctx context.Context, id int) (cr.Response, error) {
	repo, err := repositories.Fetch(ctx, srv.db, fmt.Sprintf("%d", id))
	if err != nil {
		return nil, err
	}

	return cr.Response(repo.Serialize()), nil
}

func (srv *AuthService) listRepo(ctx context.Context) (cr.Response, error) {
	repoList, err := srv.db.List(ctx, "/repositories/github/")
	if err != nil {
		return nil, fmt.Errorf("failed gettting repo with error: %w", err)
	}

	ids := extractIdFromKey(repoList, "/", 3)

	return cr.Response{"ids": ids}, nil
}

func (srv *AuthService) apiGitRepositoryServiceHandler(ctx context.Context, st streams.Connection, body command.Body) (cr.Response, error) {
	// params:
	//  TODO: add encrption key to service library
	//  action: get/set
	//  provider: github/...
	//  id
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "get":
		provider, err := maps.String(body, "provider")
		if err != nil {
			return nil, err
		}
		switch provider {
		case "github":
			hook_id, err := maps.Int(body, "id")
			if err != nil {
				return nil, err
			}
			return srv.getGithubRepositoryByID(ctx, hook_id)
		default:
			return nil, errors.New("Repository provider `" + provider + "` not supported.")
		}
	case "list":
		return srv.listRepo(ctx)
	default:
		return nil, errors.New("Repository action `" + action + "` not reconized.")
	}
}

/************** PROJECTS ***********************/

func (srv *AuthService) getProjectByID(ctx context.Context, id string) (cr.Response, error) {
	project, err := projects.Fetch(ctx, srv.db, id)
	if err != nil {
		return nil, err
	}

	return cr.Response(project.Serialize()), nil
}

func (srv *AuthService) listProjects(ctx context.Context) (cr.Response, error) {
	project, err := srv.db.List(ctx, "/projects/")
	if err != nil {
		return nil, err
	}

	ids := extractIdFromKey(project, "/", 2)

	return cr.Response{"ids": ids}, nil
}

func (srv *AuthService) apiProjectsServiceHandler(ctx context.Context, st streams.Connection, body command.Body) (cr.Response, error) {
	// params:
	//  TODO: add encrption key to service library
	//  action: get/set
	//  provider: github/...
	//  id
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "get":
		project_id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		return srv.getProjectByID(ctx, project_id)
	case "list":
		return srv.listProjects(ctx)
	default:
		return nil, errors.New("Project action `" + action + "` not reconized.")
	}
}
