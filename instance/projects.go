package instance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/frontend"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// projectTable is the register's project table as build's two roles ask it, reached
// per call through the configuration reach. A link's credentials are the vault's,
// under pkg/linkcreds' names.
type projectTable struct{ node *Instance }

func (p projectTable) table(ctx context.Context) (registerSeam, error) {
	return p.node.registers.Now(ctx)
}

// notLinked is ErrNotLinked as the connect NotFound every build caller already
// reads as "this cloud has no such thing", with the register's words kept.
func notLinked(err error) error {
	if errors.Is(err, pkgregister.ErrNotLinked) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return err
}

// Project is the project a repository belongs to: the backend's admission
// check on a push.
func (p projectTable) Project(ctx context.Context, repositoryKey string) (string, error) {
	t, err := p.table(ctx)
	if err != nil {
		return "", err
	}
	rec, err := t.Repository(ctx, repositoryKey)
	if err != nil {
		return "", notLinked(err)
	}
	return rec.Project, nil
}

// Configures says whether a repository is a project's configuration: the one
// whose pushes are deploys.
func (p projectTable) Configures(ctx context.Context, projectID, repositoryKey string) (bool, error) {
	t, err := p.table(ctx)
	if err != nil {
		return false, err
	}
	rec, err := t.Repository(ctx, repositoryKey)
	if errors.Is(err, pkgregister.ErrNotLinked) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rec.Project == projectID && rec.Configures, nil
}

// MainRepository is the repository a bare-path resource is built from: the
// configuration repository the project is claimed by. Code repositories are
// built from only when a source names them.
func (p projectTable) MainRepository(ctx context.Context, projectID string) (string, error) {
	rec, err := p.record(ctx, projectID)
	if err != nil {
		return "", err
	}
	return rec.Config, nil
}

// Repositories is every repository that gates a project.
func (p projectTable) Repositories(ctx context.Context, projectID string) ([]string, error) {
	rec, err := p.record(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return rec.Repositories(), nil
}

// ProjectRecord is the frontend's read of one project.
func (p projectTable) ProjectRecord(ctx context.Context, projectID string) (pkgregister.ProjectRecord, error) {
	return p.record(ctx, projectID)
}

func (p projectTable) record(ctx context.Context, projectID string) (pkgregister.ProjectRecord, error) {
	t, err := p.table(ctx)
	if err != nil {
		return pkgregister.ProjectRecord{}, err
	}
	rec, err := t.Project(ctx, projectID)
	return rec, notLinked(err)
}

// LinkProject, ProjectsOf and the grants are the frontend's Linker.
func (p projectTable) LinkProject(ctx context.Context, l pkgregister.Link) (pkgregister.ProjectRecord, error) {
	t, err := p.table(ctx)
	if err != nil {
		return pkgregister.ProjectRecord{}, err
	}
	return t.LinkProject(ctx, l)
}

func (p projectTable) ProjectsOf(ctx context.Context, claimant string) ([]pkgregister.ProjectRecord, error) {
	t, err := p.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.ProjectsOf(ctx, claimant)
}

// Role is the frontend's authorization: user's role on a project.
func (p projectTable) Role(ctx context.Context, projectID, user string) (pkgregister.Role, error) {
	t, err := p.table(ctx)
	if err != nil {
		return pkgregister.RoleNone, err
	}
	role, err := t.RoleOf(ctx, projectID, user)
	return role, notLinked(err)
}

func (p projectTable) GrantRole(ctx context.Context, project, grantee string, role pkgregister.Role, by string) (pkgregister.Grant, error) {
	t, err := p.table(ctx)
	if err != nil {
		return pkgregister.Grant{}, err
	}
	g, err := t.GrantRole(ctx, project, grantee, role, by)
	return g, notLinked(err)
}

func (p projectTable) Revoke(ctx context.Context, project, id, by string) error {
	t, err := p.table(ctx)
	if err != nil {
		return err
	}
	err = t.Revoke(ctx, project, id, by)
	if errors.Is(err, pkgregister.ErrNoGrant) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return err
}

func (p projectTable) Grants(ctx context.Context, project string) ([]pkgregister.Grant, error) {
	t, err := p.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Grants(ctx, project)
}

// LinkedRepositories is every repository of every linked project: the
// frontend's Linked, what a reconnected forge event stream compares heads for.
func (p projectTable) LinkedRepositories(ctx context.Context) ([]string, error) {
	t, err := p.table(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := t.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range recs {
		out = append(out, rec.Repositories()...)
	}
	return out, nil
}

// secretReader is the one call a link credential is read with.
type secretReader interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// previousReader is a vault that still serves the value a secret's newest
// version replaced, and when.
type previousReader interface {
	Previous(ctx context.Context, project, environment, secretID string) (string, time.Time, error)
}

// linkSecrets reads what a link stored in the vault.
type linkSecrets struct {
	projects projectTable
	vault    secretReader
}

// value is one link credential, or false when none is set.
func (l linkSecrets) value(ctx context.Context, project, name string) ([]byte, bool, error) {
	if l.vault == nil {
		return nil, false, secretsService.ErrNoSecrets
	}
	v, err := l.vault.Secret(ctx, project, linkcreds.Environment, name)
	switch {
	case errors.Is(err, secretsService.ErrNotSet):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return []byte(v), true, nil
}

// previous is the value a link credential's newest version replaced, or none.
func (l linkSecrets) previous(ctx context.Context, project, name string) ([]byte, time.Time, error) {
	vault, ok := l.vault.(previousReader)
	if !ok {
		return nil, time.Time{}, nil
	}
	v, at, err := vault.Previous(ctx, project, linkcreds.Environment, name)
	switch {
	case errors.Is(err, secretsService.ErrNotSet):
		return nil, time.Time{}, nil
	case err != nil:
		return nil, time.Time{}, err
	}
	return []byte(v), at, nil
}

// Hook is a project's webhook: its repositories, from the register, each with
// its own secret, from the vault. A project none of whose repositories has a
// secret set is ErrNoHook — one that can be pushed to was linked AND had a
// secret set for the repository pushed.
func (l linkSecrets) Hook(ctx context.Context, project string) (frontend.Hook, error) {
	repositories, err := l.projects.Repositories(ctx, project)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return frontend.Hook{}, fmt.Errorf("%w: %w", frontend.ErrNoHook, err)
	}
	if err != nil {
		return frontend.Hook{}, err
	}
	hook := frontend.Hook{Project: project}
	for _, repository := range repositories {
		secret, found, err := l.value(ctx, project, linkcreds.HookSecret(repository))
		if err != nil {
			return frontend.Hook{}, err
		}
		if !found {
			continue
		}
		repo := frontend.HookRepository{Key: repository, Secret: secret}
		if repo.Previous, repo.Replaced, err = l.previous(ctx, project, linkcreds.HookSecret(repository)); err != nil {
			return frontend.Hook{}, err
		}
		hook.Repositories = append(hook.Repositories, repo)
	}
	if len(hook.Repositories) == 0 {
		return frontend.Hook{}, fmt.Errorf("%w: project %s has no hook secret set", frontend.ErrNoHook, project)
	}
	return hook, nil
}

// DeployKey is a repository's deploy key, private half. A repository no
// project owns, or one with no key set, is cloned with none — which is what a
// public repository needs, and what makes a private one fail at the clone with
// the forge's own refusal.
func (l linkSecrets) DeployKey(ctx context.Context, repositoryKey string) (string, error) {
	project, err := l.projects.Project(ctx, repositoryKey)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key, _, err := l.value(ctx, project, linkcreds.DeployKey(repositoryKey))
	return string(key), err
}
