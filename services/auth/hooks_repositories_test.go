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

// Test hooks endpoints with comprehensive test data sequences
func TestHooksEndpointsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12380"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12380"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup test data then test hook endpoints
	// 1. Test hook listing workflow
	hookListResp, err := svc.listHooks(ctx)
	assert.NilError(t, err)
	assert.Assert(t, hookListResp != nil)
	assert.Assert(t, hookListResp["hooks"] != nil)

	// 2. Test hook API handler with list action
	listResp, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)

	// 3. Test hook API handler with get action but missing id
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// 4. Test hook API handler with get action and non-existent hook
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent-hook"})
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	// 5. Test hook API handler with invalid action
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid action")

	// 6. Test hook API handler with missing action
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// 7. Test hook API handler with invalid action type
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")
}

// Test repositories endpoints with comprehensive test data sequences
func TestRepositoriesEndpointsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12379"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12379"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup test data then test repository endpoints
	// 1. Test repository listing workflow
	repoListResp, err := svc.listRepo(ctx)
	assert.NilError(t, err)
	assert.Assert(t, repoListResp != nil)
	assert.Assert(t, repoListResp["ids"] != nil)

	// 2. Test repository API handler with list action
	listResp, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)

	// 3. Test repository API handler with get action but missing provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing provider in get action")

	// 4. Test repository API handler with get action and unsupported provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "gitlab"})
	assert.Assert(t, err != nil, "Expected error for unsupported provider")

	// 5. Test repository API handler with get action and missing id
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// 6. Test repository API handler with get action and invalid id type
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github", "id": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid id type")

	// 7. Test repository API handler with invalid action
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid action")

	// 8. Test repository API handler with missing action
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// 9. Test repository API handler with invalid action type
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")
}

// Test repository hook business logic functions
func TestRepositoryHookBusinessLogic(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12365"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12365"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getRepositoryHookByID with non-existent hook
	_, err = svc.getRepositoryHookByID(ctx, "non-existent-hook")
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	// Test getGithubRepositoryByID with non-existent repo
	_, err = svc.getGithubRepositoryByID(ctx, 999)
	assert.Assert(t, err != nil, "Expected error for non-existent repository")

	// Test apiHookServiceHandler with get action
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get", "id": "non-existent"})
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	// Test apiGitRepositoryServiceHandler with get action for github provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github", "id": 999})
	assert.Assert(t, err != nil, "Expected error for non-existent repository")

	// Test apiGitRepositoryServiceHandler with unsupported provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "gitlab", "id": 999})
	assert.Assert(t, err != nil, "Expected error for unsupported provider")
}

// Test repository hook functions
func TestRepositoryHookFunctions(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12360"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12360"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getRepositoryHookByID with non-existent hook
	_, err = svc.getRepositoryHookByID(ctx, "non-existent-hook")
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	// Test getGithubRepositoryByID with non-existent repo
	_, err = svc.getGithubRepositoryByID(ctx, 999)
	assert.Assert(t, err != nil, "Expected error for non-existent repository")
}

// Test hook and repository API functions
func TestHookAndRepositoryFunctions(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12358"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12358"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test listHooks
	hooksResp, err := svc.listHooks(ctx)
	assert.NilError(t, err)
	assert.Assert(t, hooksResp != nil)

	// Test listRepo
	repoResp, err := svc.listRepo(ctx)
	assert.NilError(t, err)
	assert.Assert(t, repoResp != nil)

	// Test apiHookServiceHandler with list action
	hookListResp, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, hookListResp != nil)

	// Test apiHookServiceHandler with invalid action
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid hook action")

	// Test apiGitRepositoryServiceHandler with list action
	repoListResp, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, repoListResp != nil)

	// Test apiGitRepositoryServiceHandler with invalid action
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid repository action")
}

// Test apiHookServiceHandler with different action scenarios
func TestApiHookServiceHandlerScenarios(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12369"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12369"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test with missing action
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// Test with invalid action type
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")

	// Test with get action but missing id
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// Test with get action and valid id
	_, err = svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "get", "id": "test-hook"})
	assert.Assert(t, err != nil, "Expected error for non-existent hook")

	// Test with list action
	listResp, err := svc.apiHookServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)
}

// Test apiGitRepositoryServiceHandler with different action scenarios
func TestApiGitRepositoryServiceHandlerScenarios(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12370"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12370"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test with missing action
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// Test with invalid action type
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")

	// Test with get action but missing provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get"})
	assert.Assert(t, err != nil, "Expected error for missing provider in get action")

	// Test with get action and unsupported provider
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "gitlab"})
	assert.Assert(t, err != nil, "Expected error for unsupported provider")

	// Test with get action and missing id
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github"})
	assert.Assert(t, err != nil, "Expected error for missing id in get action")

	// Test with get action and invalid id type
	_, err = svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "get", "provider": "github", "id": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid id type")

	// Test with list action
	listResp, err := svc.apiGitRepositoryServiceHandler(ctx, nil, command.Body{"action": "list"})
	assert.NilError(t, err)
	assert.Assert(t, listResp != nil)
}
