// Package linked is what a link wrote, as build's two halves read it: the register's
// project table, reached per call, and the credentials the link stored in the vault
// under pkg/linkcreds' names.
package linked

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
)

// Projects is the register's project table. Table is how it is reached: per call,
// since which node answers is a routing question.
type Projects struct {
	Table func(ctx context.Context) (register.Table, error)
}

// notLinked is ErrNotLinked as the connect NotFound every build caller already
// reads as "this cloud has no such thing", with the register's words kept.
func notLinked(err error) error {
	if errors.Is(err, pkgregister.ErrNotLinked) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return err
}

// Project is the project a repository belongs to: build's admission check on
// a push.
func (p Projects) Project(ctx context.Context, repositoryKey string) (string, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return "", err
	}
	rec, err := t.Repository(ctx, repositoryKey)
	if err != nil {
		return "", notLinked(err)
	}
	return rec.Project, nil
}

// Configures says whether a repository is a project's configuration: the one whose
// pushes are deploys.
func (p Projects) Configures(ctx context.Context, projectID, repositoryKey string) (bool, error) {
	t, err := p.Table(ctx)
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
// configuration repository the project is claimed by. Code repositories are built
// from only when a source names them.
func (p Projects) MainRepository(ctx context.Context, projectID string) (string, error) {
	rec, err := p.ProjectRecord(ctx, projectID)
	if err != nil {
		return "", err
	}
	return rec.Config, nil
}

// Repositories is every repository that gates a project.
func (p Projects) Repositories(ctx context.Context, projectID string) ([]string, error) {
	rec, err := p.ProjectRecord(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return rec.Repositories(), nil
}

// ProjectRecord is one project as the table holds it.
func (p Projects) ProjectRecord(ctx context.Context, projectID string) (pkgregister.ProjectRecord, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return pkgregister.ProjectRecord{}, err
	}
	rec, err := t.Project(ctx, projectID)
	return rec, notLinked(err)
}

// LinkProject writes a link: ingress's, after authorizing the caller.
func (p Projects) LinkProject(ctx context.Context, l pkgregister.Link) (pkgregister.ProjectRecord, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return pkgregister.ProjectRecord{}, err
	}
	return t.LinkProject(ctx, l)
}

// ProjectsOf is every project a claimant linked.
func (p Projects) ProjectsOf(ctx context.Context, claimant string) ([]pkgregister.ProjectRecord, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return nil, err
	}
	return t.ProjectsOf(ctx, claimant)
}

// Role is a user's role on a project.
func (p Projects) Role(ctx context.Context, projectID, user string) (pkgregister.Role, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return pkgregister.RoleNone, err
	}
	role, err := t.RoleOf(ctx, projectID, user)
	return role, notLinked(err)
}

func (p Projects) GrantRole(ctx context.Context, project, grantee string, role pkgregister.Role, by string) (pkgregister.Grant, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return pkgregister.Grant{}, err
	}
	g, err := t.GrantRole(ctx, project, grantee, role, by)
	return g, notLinked(err)
}

func (p Projects) Revoke(ctx context.Context, project, id, by string) error {
	t, err := p.Table(ctx)
	if err != nil {
		return err
	}
	err = t.Revoke(ctx, project, id, by)
	if errors.Is(err, pkgregister.ErrNoGrant) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return err
}

func (p Projects) Grants(ctx context.Context, project string) ([]pkgregister.Grant, error) {
	t, err := p.Table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Grants(ctx, project)
}

// LinkedRepositories is every repository of every linked project: what a
// reconnected forge event stream compares heads for.
func (p Projects) LinkedRepositories(ctx context.Context) ([]string, error) {
	t, err := p.Table(ctx)
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

// Vault is the one call a link credential is read with.
type Vault interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// previousReader is a vault that still serves the value a secret's newest version
// replaced, and when.
type previousReader interface {
	Previous(ctx context.Context, project, environment, secretID string) (string, time.Time, error)
}

// Credentials reads what a link stored in the vault.
type Credentials struct {
	Projects Projects
	Vault    Vault
}

// Value is one link credential, or false when none is set.
func (c Credentials) Value(ctx context.Context, project, name string) ([]byte, bool, error) {
	if c.Vault == nil {
		return nil, false, secrets.ErrNoSecrets
	}
	v, err := c.Vault.Secret(ctx, project, linkcreds.Environment, name)
	switch {
	case errors.Is(err, secrets.ErrNotSet):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return []byte(v), true, nil
}

// Previous is the value a link credential's newest version replaced, or none.
func (c Credentials) Previous(ctx context.Context, project, name string) ([]byte, time.Time, error) {
	vault, ok := c.Vault.(previousReader)
	if !ok {
		return nil, time.Time{}, nil
	}
	v, at, err := vault.Previous(ctx, project, linkcreds.Environment, name)
	switch {
	case errors.Is(err, secrets.ErrNotSet):
		return nil, time.Time{}, nil
	case err != nil:
		return nil, time.Time{}, err
	}
	return []byte(v), at, nil
}

// DeployKey is a repository's deploy key, private half. A repository no project
// owns, or one with no key set, is cloned with none — which is what a public
// repository needs, and what makes a private one fail at the clone with the forge's
// own refusal.
func (c Credentials) DeployKey(ctx context.Context, repositoryKey string) (string, error) {
	project, err := c.Projects.Project(ctx, repositoryKey)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key, _, err := c.Value(ctx, project, linkcreds.DeployKey(repositoryKey))
	return string(key), err
}
