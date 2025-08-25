package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/pkg/kvdb/mock"

	"gotest.tools/v3/assert"
)

// Test projects endpoints with comprehensive test data sequences
func TestProjectsEndpointsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12378"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12378"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup test data then test project endpoints
	// 1. Test project listing workflow
	projectListResp, err := svc.listProjects(ctx)
	assert.NilError(t, err)
	assert.Assert(t, projectListResp != nil)
	assert.Assert(t, projectListResp["ids"] != nil)

	// 2. Test project API handler with list action
	listResp, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)

	// 3. Test project API handler with get action but missing id
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// 4. Test project API handler with get action and non-existent project
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent-project"})
	assert.Assert(t, err != nil, "Expected error for non-existent project")

	// 5. Test project API handler with invalid action
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid action")

	// 6. Test project API handler with missing action
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// 7. Test project API handler with invalid action type
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")
}

// Test project error handling and edge cases
func TestProjectErrorHandling(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12364"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12364"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getProjectByID with non-existent project
	_, err = svc.getProjectByID(ctx, "non-existent-project")
	assert.Assert(t, err != nil, "Expected error for non-existent project")

	// Test listProjects - this should work and return empty list
	projectsResp, err := svc.listProjects(ctx)
	assert.NilError(t, err)
	assert.Assert(t, projectsResp != nil)
	assert.Assert(t, projectsResp["ids"] != nil)

	// Test apiProjectsServiceHandler with get action
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent"})
	assert.Assert(t, err != nil, "Expected error for non-existent project")

	// Test apiProjectsServiceHandler with list action
	listResp, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)

	// Test apiProjectsServiceHandler with invalid action
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid project action")
}

// Test apiProjectsServiceHandler with different input validation scenarios
func TestApiProjectsServiceHandlerInputValidation(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12368"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12368"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test with missing action
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// Test with invalid action type
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")

	// Test with get action but missing id
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// Test with get action and valid id
	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get", "id": "test-project"})
	assert.Assert(t, err != nil, "Expected error for non-existent project")

	// Test with list action
	listResp, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)
}

// Test project and repository API operations with sub-tests
func TestProjectAndRepositoryAPIOperationsWithSubTests(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12386"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12386"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getProjectByID with non-existent project
	t.Run("getProjectByID", func(t *testing.T) {
		_, err := svc.getProjectByID(ctx, "non-existent-project")
		assert.Assert(t, err != nil, "Expected error for non-existent project")
	})

	// Test listProjects
	t.Run("listProjects", func(t *testing.T) {
		projects, err := svc.listProjects(ctx)
		assert.NilError(t, err)
		assert.Assert(t, projects != nil)
	})

	// Test apiProjectsServiceHandler with list action
	t.Run("apiProjectsServiceHandler", func(t *testing.T) {
		projects, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "list"})
		assert.NilError(t, err)
		assert.Assert(t, projects != nil)
	})

	// Test apiProjectsServiceHandler with invalid action
	t.Run("apiProjectsServiceHandlerInvalidAction", func(t *testing.T) {
		_, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "invalid"})
		assert.Assert(t, err != nil, "Expected error for invalid action")
	})

	// Test getGithubRepositoryByID with non-existent repo
	t.Run("getGithubRepositoryByID", func(t *testing.T) {
		_, err := svc.getGithubRepositoryByID(ctx, 999)
		assert.Assert(t, err != nil, "Expected error for non-existent repository")
	})

	// Test listRepo
	t.Run("listRepo", func(t *testing.T) {
		repos, err := svc.listRepo(ctx)
		assert.NilError(t, err)
		assert.Assert(t, repos != nil)
	})

	// Test apiGitRepositoryServiceHandler with list action
	t.Run("apiGitRepositoryServiceHandler", func(t *testing.T) {
		repos, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "list"})
		assert.NilError(t, err)
		assert.Assert(t, repos != nil)
	})

	// Test apiGitRepositoryServiceHandler with invalid action
	t.Run("apiGitRepositoryServiceHandlerInvalidAction", func(t *testing.T) {
		_, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "invalid"})
		assert.Assert(t, err != nil, "Expected error for invalid action")
	})

	// Test getRepositoryHookByID with non-existent hook
	t.Run("getRepositoryHookByID", func(t *testing.T) {
		_, err := svc.getRepositoryHookByID(ctx, "non-existent-hook")
		assert.Assert(t, err != nil, "Expected error for non-existent hook")
	})

	// Test listHooks
	t.Run("listHooks", func(t *testing.T) {
		hooks, err := svc.listHooks(ctx)
		assert.NilError(t, err)
		assert.Assert(t, hooks != nil)
	})

	// Test apiHookServiceHandler with list action
	t.Run("apiHookServiceHandler", func(t *testing.T) {
		hooks, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "list"})
		assert.NilError(t, err)
		assert.Assert(t, hooks != nil)
	})

	// Test apiHookServiceHandler with invalid action
	t.Run("apiHookServiceHandlerInvalidAction", func(t *testing.T) {
		_, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "invalid"})
		assert.Assert(t, err != nil, "Expected error for invalid action")
	})
}

// Test repository and project workflows with comprehensive test data sequences
func TestRepositoryProjectWorkflowsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12375"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12375"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup test data then test workflows
	// 1. Test repository listing workflow
	repoListResp, err := svc.listRepo(ctx)
	assert.NilError(t, err)
	assert.Assert(t, repoListResp != nil)

	// 2. Test hook listing workflow
	hookListResp, err := svc.listHooks(ctx)
	assert.NilError(t, err)
	assert.Assert(t, hookListResp != nil)

	// 3. Test project listing workflow
	projectListResp, err := svc.listProjects(ctx)
	assert.NilError(t, err)
	assert.Assert(t, projectListResp != nil)

	// 4. Test API handlers with list actions
	repoHandlerResp, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, repoHandlerResp != nil)

	hookHandlerResp, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, hookHandlerResp != nil)

	projectHandlerResp, err := svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, projectHandlerResp != nil)

	// 5. Test error cases for get actions
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github", "id": 999})
	assert.Assert(t, err != nil, "Expected error for non-existent repository")

	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent-hook"})
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	_, err = svc.apiProjectsServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent-project"})
	assert.Assert(t, err != nil, "Expected error for non-existent project")
}
