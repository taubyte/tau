package auth

import (
	"context"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/utils/id"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// projectHandler implements authv2connect.ProjectServiceHandler. See
// repositories.go's repositoryHandler doc for why this is its own type
// rather than a method set on *Service.
type projectHandler struct{ s *Service }

var _ authv2connect.ProjectServiceHandler = (*projectHandler)(nil)

// Get returns a project by id.
func (h *projectHandler) Get(ctx context.Context, req *connect.Request[authv2.ProjectRef]) (*connect.Response[authv2.Project], error) {
	projectID := req.Msg.GetId()
	if err := requireID("id", projectID); err != nil {
		return nil, err
	}
	record, err := fetchProject(ctx, h.s.kv, projectID)
	if err != nil {
		return nil, errNotFound("%w", err)
	}
	return connect.NewResponse(&authv2.Project{
		Id:                  projectID,
		Name:                record.name,
		ConfigRepositoryKey: record.config,
		CodeRepositoryKey:   record.code,
	}), nil
}

// List streams the id of every registered project.
func (h *projectHandler) List(ctx context.Context, _ *connect.Request[authv2.Empty], stream *connect.ServerStream[authv2.ProjectRef]) error {
	ids, err := listProjectIDs(ctx, h.s.kv)
	if err != nil {
		return errInternal("listing projects: %w", err)
	}
	for _, projectID := range ids {
		if err := stream.Send(&authv2.ProjectRef{Id: projectID}); err != nil {
			return err
		}
	}
	return nil
}

// Create registers a new project and links its two repositories to it.
//
// NO OWNERSHIP CHECK GUARDS config_repository_key / code_repository_key.
// v1's p2p path for this (services/auth/api_hooks.go's createProjectStream)
// has none either — the GitHub-token HTTP path (out of scope here) is the
// one that requires write access to both repositories
// (services/auth/github.go's newGitHubProject) — so this matches v1's own
// p2p behaviour rather than inventing a new requirement. It is a
// deliberately narrower fix than Register/Unregister got: those manage a
// SECRET (a deploy key) whose lifecycle this package can reason about from
// peer identity alone, where Create only links two key STRINGS to a new id,
// and requiring the caller here to be the same peer that Registered both
// repositories would assume something the schema does not say — that the
// service creating a project is always the same one that registered its
// repositories — which nothing in scope establishes. Per authz.go, this is
// the documented OSS default: any authenticated peer may call it.
func (h *projectHandler) Create(ctx context.Context, req *connect.Request[authv2.CreateProjectRequest]) (*connect.Response[authv2.CreateProjectResponse], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, errInvalidArgument("name is required")
	}

	configKey, err := parseRepoKey(req.Msg.GetConfigRepositoryKey())
	if err != nil {
		return nil, errInvalidArgument("config_repository_key: %w", err)
	}
	codeKey, err := parseRepoKey(req.Msg.GetCodeRepositoryKey())
	if err != nil {
		return nil, errInvalidArgument("code_repository_key: %w", err)
	}
	for _, key := range []repokey.Key{configKey, codeKey} {
		if err := h.s.tenancy.Refuse(key); err != nil {
			return nil, errPermissionDenied("%w", err)
		}
	}

	projectID := id.Generate(name, configKey.String(), codeKey.String())

	if err := putProject(ctx, h.s.kv, projectID, name, configKey, codeKey); err != nil {
		return nil, errInternal("creating project: %w", err)
	}

	// The link is written unconditionally, the same as v1's
	// createProjectStream (store.go's linkRepositoryToProject explains why
	// that is safe even when a key was never separately Registered).
	if err := linkRepositoryToProject(ctx, h.s.kv, configKey, projectID); err != nil {
		return nil, errInternal("linking config repository: %w", err)
	}
	if err := linkRepositoryToProject(ctx, h.s.kv, codeKey, projectID); err != nil {
		return nil, errInternal("linking code repository: %w", err)
	}

	return connect.NewResponse(&authv2.CreateProjectResponse{
		Id:   projectID,
		Name: name,
	}), nil
}
