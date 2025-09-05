package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/tau/utils/maps"
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
	case "register":
		return srv.registerRepositoryStream(ctx, body)
	case "unregister":
		return srv.unregisterRepositoryStream(ctx, body)
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

// registerRepositoryStream handles repository registration over p2p streams
func (srv *AuthService) registerRepositoryStream(ctx context.Context, body command.Body) (cr.Response, error) {
	provider, err := maps.String(body, "provider")
	if err != nil {
		return nil, fmt.Errorf("missing provider parameter: %w", err)
	}

	if provider != "github" {
		return nil, fmt.Errorf("provider `%s` is not supported", provider)
	}

	repoID, err := maps.String(body, "id")
	if err != nil {
		return nil, fmt.Errorf("missing repository ID parameter: %w", err)
	}

	// For p2p streams (internal service communication), we can register repositories
	// without external GitHub API calls since services trust each other
	// Use the main function with nil client to skip GitHub API verification
	response, err := srv.registerGitHubRepository(ctx, nil, repoID)
	if err != nil {
		return nil, fmt.Errorf("repository registration failed: %w", err)
	}

	return cr.Response{
		"key": response.Key,
	}, nil
}

// unregisterRepositoryStream handles repository unregistration over p2p streams
func (srv *AuthService) unregisterRepositoryStream(ctx context.Context, body command.Body) (cr.Response, error) {
	provider, err := maps.String(body, "provider")
	if err != nil {
		return nil, fmt.Errorf("missing provider parameter: %w", err)
	}

	if provider != "github" {
		return nil, fmt.Errorf("provider `%s` is not supported", provider)
	}

	repoID, err := maps.String(body, "id")
	if err != nil {
		return nil, fmt.Errorf("missing repository ID parameter: %w", err)
	}

	// For p2p streams (internal service communication), we can unregister repositories
	// without external GitHub API calls
	// Use the main function with nil client to skip GitHub API verification
	err = srv.unregisterGitHubRepository(ctx, nil, repoID)
	if err != nil {
		return nil, fmt.Errorf("repository unregistration failed: %w", err)
	}

	return cr.Response{"status": "success"}, nil
}

/******* DOMAIN ********/
// ApiDomainServiceHandler handles domain-related p2p stream requests
func (srv *AuthService) ApiDomainServiceHandler(ctx context.Context, st streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}
	switch action {
	case "register":
		return srv.registerDomainStream(ctx, body)
	default:
		return nil, errors.New("Domain action `" + action + "` not recognized.")
	}
}

// registerDomainStream handles domain registration over p2p streams
func (srv *AuthService) registerDomainStream(_ context.Context, body command.Body) (cr.Response, error) {
	fqdn, err := maps.String(body, "fqdn")
	if err != nil {
		return nil, fmt.Errorf("missing fqdn parameter: %w", err)
	}

	projectID, err := maps.String(body, "project")
	if err != nil {
		return nil, fmt.Errorf("missing project parameter: %w", err)
	}

	if len(projectID) < 8 {
		return nil, errors.New("project ID is too short")
	}

	project, err := cid.Decode(projectID)
	if err != nil {
		return nil, fmt.Errorf("decode project ID failed with %w", err)
	}

	claim, err := domainValidationNew(fqdn, project, srv.dvPrivateKey, srv.dvPublicKey)
	if err != nil {
		return nil, fmt.Errorf("new domain validation failed with: %s", err)
	}

	token, err := claim.Sign()
	if err != nil {
		return nil, fmt.Errorf("signing claim failed with: %s", err)
	}

	return cr.Response{
		"token": string(token),
		"entry": fmt.Sprintf("%s.%s", projectID[:8], fqdn),
		"type":  "txt",
	}, nil
}
