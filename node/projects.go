package node

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/frontend"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// projects.go is the register's project table as build's two roles ask it.
//
// EVERY QUESTION ABOUT WHO A PROJECT IS GOES TO ONE PLACE. Which repository a
// push belongs to, whether it is the project's configuration, which
// repositories gate a caller, and what a link writes are all reads and one
// write of the table pkg/register's projects.go keeps — reached per call
// through the same reach every configuration read uses, so the answer comes
// from whichever register node is routable now.
//
// The credentials a link stored are the vault's, under pkg/linkcreds' names;
// the table says which project a repository is, and the vault holds its keys.
type projectTable struct{ node *Node }

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

// CodeRepository is the repository a bare-path resource is built from: the
// project's first code repository, or none.
func (p projectTable) CodeRepository(ctx context.Context, projectID string) (string, error) {
	rec, err := p.record(ctx, projectID)
	if err != nil {
		return "", err
	}
	if len(rec.Code) == 0 {
		return "", nil
	}
	return rec.Code[0], nil
}

// Repositories is every repository that gates a project.
func (p projectTable) Repositories(ctx context.Context, projectID string) ([]string, error) {
	rec, err := p.record(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return rec.Repositories(), nil
}

func (p projectTable) record(ctx context.Context, projectID string) (pkgregister.ProjectRecord, error) {
	t, err := p.table(ctx)
	if err != nil {
		return pkgregister.ProjectRecord{}, err
	}
	rec, err := t.Project(ctx, projectID)
	return rec, notLinked(err)
}

// LinkProject and ProjectsOf are the frontend's Linker.
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

// secretReader is the one call a link credential is read with.
type secretReader interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// linkSecrets reads what a link stored in the vault.
type linkSecrets struct {
	projects projectTable
	vault    secretReader
}

// value is one link credential, or false when none is set.
func (l linkSecrets) value(ctx context.Context, project, name string) ([]byte, bool, error) {
	if l.vault == nil {
		return nil, false, ErrNoSecrets
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

// Hook is a project's webhook: its repositories, from the register, and its
// secret, from the vault. Either missing is ErrNoHook — a project that can be
// pushed to is one that was linked AND had its secret set.
func (l linkSecrets) Hook(ctx context.Context, project string) (frontend.Hook, error) {
	repositories, err := l.projects.Repositories(ctx, project)
	if errors.Is(err, pkgregister.ErrNotLinked) {
		return frontend.Hook{}, fmt.Errorf("%w: %w", frontend.ErrNoHook, err)
	}
	if err != nil {
		return frontend.Hook{}, err
	}
	secret, found, err := l.value(ctx, project, linkcreds.HookSecret)
	if err != nil {
		return frontend.Hook{}, err
	}
	if !found {
		return frontend.Hook{}, fmt.Errorf("%w: project %s has no hook secret set", frontend.ErrNoHook, project)
	}
	return frontend.Hook{Project: project, Repositories: repositories, Secret: secret}, nil
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
