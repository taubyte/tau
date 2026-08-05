package auth

import (
	"context"
	"testing"

	"github.com/google/go-github/v71/github"
	"gotest.tools/v3/assert"
)

// authzClient is a GitHubClient for one identity, holding the repositories that
// identity can write to. Repositories outside `writable` still resolve — that is
// what a public repository does — but carry no write permission.
type authzClient struct {
	GitHubClient
	id       int64
	login    string
	writable map[string]bool
	cur      *github.Repository
}

func (c *authzClient) Me() *github.User {
	return &github.User{ID: &c.id, Login: &c.login}
}

func (c *authzClient) GetByID(id string) error {
	c.cur = &github.Repository{
		ID:          &c.id,
		FullName:    github.Ptr("someone/" + id),
		Permissions: map[string]bool{"pull": true, "push": c.writable[id], "admin": c.writable[id]},
	}
	return nil
}

func (c *authzClient) Cur() *github.Repository { return c.cur }

func (c *authzClient) ShortRepositoryInfo(id string) RepositoryShortInfo {
	return RepositoryShortInfo{ID: id}
}

func newAuthzService(t *testing.T, port int) *AuthService {
	t.Helper()
	svc, err := New(context.Background(), newTestConfig(t, port))
	assert.NilError(t, err)
	t.Cleanup(func() { svc.Close() })
	// The authorization checks are skipped in dev mode, as every other check in
	// this service is; these tests are about the production path.
	svc.devMode = false
	return svc
}

// TestProjectAccessByID covers taubyte/tau#513: GET and DELETE by project id
// must refuse a caller who cannot write to either linked repository.
func TestProjectAccessByID(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzService(t, 13513)

	victim := &authzClient{id: 1, login: "victim", writable: map[string]bool{"1001": true, "1002": true}}
	attacker := &authzClient{id: 2, login: "attacker", writable: map[string]bool{"9001": true}}

	_, err := svc.newGitHubProject(ctx, victim, "PROJECT_ID_513", "victims-project", "1001", "1002")
	assert.NilError(t, err)

	_, err = svc.getGitHubProjectInfo(ctx, attacker, "PROJECT_ID_513")
	assert.Error(t, err, "project not found")

	_, err = svc.deleteGitHubUserProject(ctx, attacker, "PROJECT_ID_513")
	assert.Error(t, err, "project not found")

	// The owner still gets through, and the project survived the attempts.
	info, err := svc.getGitHubProjectInfo(ctx, victim, "PROJECT_ID_513")
	assert.NilError(t, err)
	assert.Equal(t, info.Project.Name, "victims-project")

	_, err = svc.deleteGitHubUserProject(ctx, victim, "PROJECT_ID_513")
	assert.NilError(t, err)
}

// TestProjectImportOverExisting covers the takeover path: import supplies the
// project id, so it must not overwrite a project the caller has no access to.
func TestProjectImportOverExisting(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzService(t, 13514)

	victim := &authzClient{id: 1, login: "victim", writable: map[string]bool{"1001": true, "1002": true}}
	attacker := &authzClient{id: 2, login: "attacker", writable: map[string]bool{"9001": true, "9002": true}}

	_, err := svc.newGitHubProject(ctx, victim, "PROJECT_ID_513", "victims-project", "1001", "1002")
	assert.NilError(t, err)

	// Attacker's own repositories, victim's project id.
	_, err = svc.newGitHubProject(ctx, attacker, "PROJECT_ID_513", "attackers-project", "9001", "9002")
	assert.Error(t, err, "project not found")

	info, err := svc.getGitHubProjectInfo(ctx, victim, "PROJECT_ID_513")
	assert.NilError(t, err)
	assert.Equal(t, info.Project.Name, "victims-project")
	assert.Equal(t, info.Project.Repositories.Configuration.ID, "1001")

	_, err = svc.db.Get(ctx, "/projects/PROJECT_ID_513/owners/2")
	assert.Assert(t, err != nil, "attacker recorded itself as an owner")

	// The victim's repository still points at the victim's project.
	linked, err := svc.db.Get(ctx, "/repositories/github/1001/project")
	assert.NilError(t, err)
	assert.Equal(t, string(linked), "PROJECT_ID_513")
}

// TestNewProjectRequiresRepoAccess: a project may only be created against
// repositories the caller can write to.
func TestNewProjectRequiresRepoAccess(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzService(t, 13515)

	attacker := &authzClient{id: 2, login: "attacker", writable: map[string]bool{"9001": true}}

	// 1002 is readable (public) but not writable by the caller.
	_, err := svc.newGitHubProject(ctx, attacker, "PROJECT_ID_NEW", "grab", "9001", "1002")
	assert.Error(t, err, "no access to repository `1002`")

	_, err = svc.db.Get(ctx, "/repositories/github/1002/project")
	assert.Assert(t, err != nil, "back-reference written despite refusal")
}

// TestUnregisterRepositoryRequiresAccess: read access to a public repository is
// not enough to tear down its deploy key and hooks.
func TestUnregisterRepositoryRequiresAccess(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzService(t, 13516)

	attacker := &authzClient{id: 2, login: "attacker", writable: map[string]bool{}}

	err := svc.unregisterGitHubRepository(ctx, attacker, "1001")
	assert.Error(t, err, "no access to repository `1001`")
}
