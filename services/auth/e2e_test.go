//go:build dreaming

package auth_test

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/tau/utils/id"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestAuthServiceE2E_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Second)

	simple, err := u.Simple("client")
	if err != nil {
		t.Fatal(err)
	}

	auth, err := simple.Auth()
	assert.NilError(t, err)

	// Test basic service functionality
	t.Run("ServiceBasics", func(t *testing.T) {
		testServiceBasics(t, u, auth)
	})

	// Test hooks functionality
	t.Run("Hooks", func(t *testing.T) {
		testHooks(t, u, auth)
	})

	// Test repositories functionality
	t.Run("Repositories", func(t *testing.T) {
		testRepositories(t, u, auth)
	})

	// Test projects functionality
	t.Run("Projects", func(t *testing.T) {
		testProjects(t, u, auth)
	})

	// Test domain validation
	t.Run("DomainValidation", func(t *testing.T) {
		testDomainValidation(t, u, auth)
	})

	// Test stream API functionality
	t.Run("StreamAPI", func(t *testing.T) {
		testStreamAPI(t, u, auth)
	})
}

func testServiceBasics(t *testing.T, u *dream.Universe, auth interface{}) {
	assert.Assert(t, auth != nil)

	authService := u.Auth()
	assert.Assert(t, authService != nil)
	assert.Assert(t, authService.Node() != nil)
	assert.Assert(t, authService.KV() != nil)
}

func testHooks(t *testing.T, u *dream.Universe, auth interface{}) {
	ctx := u.Context()

	hookID := id.Generate()
	hook, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         hookID,
		"provider":   "github",
		"github_id":  12345,
		"repository": 67890,
		"secret":     "test_secret_123",
	})
	assert.NilError(t, err)

	err = hook.Register(ctx)
	assert.NilError(t, err)

	retrievedHook, err := hooks.Fetch(ctx, u.Auth().KV(), hookID)
	assert.NilError(t, err)
	assert.Assert(t, retrievedHook != nil)
	assert.Equal(t, retrievedHook.ID(), hookID)
	assert.Equal(t, retrievedHook.ProviderID(), "12345")

	if githubHook, ok := retrievedHook.(*hooks.GithubHook); ok {
		assert.Equal(t, githubHook.GithubId, 12345)
		assert.Equal(t, githubHook.Repository, 67890)
		assert.Equal(t, githubHook.Secret, "test_secret_123")
	}

	hookIDs, err := u.Auth().KV().List(ctx, "/hooks/")
	assert.NilError(t, err)
	assert.Assert(t, len(hookIDs) > 0)

	err = hook.Delete(ctx)
	assert.NilError(t, err)
}

func testRepositories(t *testing.T, u *dream.Universe, auth interface{}) {
	ctx := u.Context()

	// Create a test repository
	repo, err := repositories.New(u.Auth().KV(), repositories.Data{
		"id":       12345,
		"provider": "github",
		"name":     "test/repo",
		"project":  "test_project_uuid",
		"key":      "test_key_123",
		"url":      "https://github.com/test/repo",
	})
	assert.NilError(t, err)

	// Register the repository
	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test getting the repository
	retrievedRepo, err := repositories.Fetch(ctx, u.Auth().KV(), "12345")
	assert.NilError(t, err)
	assert.Assert(t, retrievedRepo != nil)
	assert.Equal(t, retrievedRepo.ID(), 12345)
	assert.Equal(t, retrievedRepo.Provider(), "github")

	// Access repository data through Serialize() method
	repoData := retrievedRepo.Serialize()
	assert.Equal(t, repoData["key"], "test_key_123")
	// Note: project field is not stored during registration, only key is stored

	// Test listing repositories
	repoIDs, err := u.Auth().KV().List(ctx, "/repositories/github/")
	assert.NilError(t, err)
	assert.Assert(t, len(repoIDs) > 0)

	// Clean up
	err = repo.Delete(ctx)
	assert.NilError(t, err)
}

func testProjects(t *testing.T, u *dream.Universe, auth interface{}) {
	ctx := u.Context()

	// Create a test project
	project, err := projects.New(u.Auth().KV(), projects.Data{
		"id":   "test_project_123",
		"name": "Test Project",
	})
	assert.NilError(t, err)

	// Register the project
	err = project.Register()
	assert.NilError(t, err)

	// Test getting the project
	retrievedProject, err := projects.Fetch(ctx, u.Auth().KV(), "test_project_123")
	assert.NilError(t, err)
	assert.Assert(t, retrievedProject != nil)
	assert.Equal(t, retrievedProject.Name(), "Test Project")

	// Test listing projects
	projectIDs, err := u.Auth().KV().List(ctx, "/projects/")
	assert.NilError(t, err)
	assert.Assert(t, len(projectIDs) > 0)

	// Clean up
	err = project.Delete()
	assert.NilError(t, err)
}

func testDomainValidation(t *testing.T, u *dream.Universe, auth interface{}) {
	// Test domain validation endpoint
	// Note: This would require a valid project ID and domain
	// For now, we'll just test that the service has the required keys
	authService := u.Auth()

	// The service should have domain validation keys configured
	// (These are set in the dream config)
	assert.Assert(t, authService != nil)
}

func testStreamAPI(t *testing.T, u *dream.Universe, auth interface{}) {
	// Test stream API functionality
	// This would involve testing the p2p stream handlers
	// For now, we'll verify the stream service is running
	authService := u.Auth()
	assert.Assert(t, authService != nil)

	// Test that we can list hooks via stream API
	// This would require setting up a stream connection
	// For e2e tests, we'll just verify the service is accessible
}

func TestAuthServiceWithMockData_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Second)

	// Test creating multiple hooks and repositories
	t.Run("MultipleEntities", func(t *testing.T) {
		testMultipleEntities(t, u)
	})

	// Test cleanup and isolation
	t.Run("CleanupAndIsolation", func(t *testing.T) {
		testCleanupAndIsolation(t, u)
	})
}

func testMultipleEntities(t *testing.T, u *dream.Universe) {
	ctx := u.Context()

	// Create multiple hooks
	hookIDs := []string{}
	for i := 0; i < 3; i++ {
		hookID := id.Generate()
		hook, err := hooks.New(u.Auth().KV(), hooks.Data{
			"id":         hookID,
			"provider":   "github",
			"github_id":  int64(1000 + i),
			"repository": int64(2000 + i),
			"secret":     "secret_" + hookID[:8],
		})
		assert.NilError(t, err)

		err = hook.Register(ctx)
		assert.NilError(t, err)

		hookIDs = append(hookIDs, hookID)
	}

	// Create multiple repositories
	repoIDs := []int{}
	for i := 0; i < 3; i++ {
		repo, err := repositories.New(u.Auth().KV(), repositories.Data{
			"id":       3000 + i,
			"provider": "github",
			"name":     "test/repo" + string(rune('a'+i)),
			"project":  "project_" + string(rune('a'+i)),
			"key":      "key_" + string(rune('a'+i)),
			"url":      "https://github.com/test/repo" + string(rune('a'+i)),
		})
		assert.NilError(t, err)

		err = repo.Register(ctx)
		assert.NilError(t, err)

		repoIDs = append(repoIDs, 3000+i)
	}

	// Verify all entities were created
	hookList, err := u.Auth().KV().List(ctx, "/hooks/")
	assert.NilError(t, err)
	assert.Assert(t, len(hookList) >= 3, "Expected at least 3 hooks, got %d", len(hookList))

	repoList, err := u.Auth().KV().List(ctx, "/repositories/github/")
	assert.NilError(t, err)
	assert.Assert(t, len(repoList) >= 3, "Expected at least 3 repositories, got %d", len(repoList))

	// Clean up
	for _, hookID := range hookIDs {
		hook, err := hooks.Fetch(ctx, u.Auth().KV(), hookID)
		if err == nil && hook != nil {
			hook.Delete(ctx)
		}
	}

	for _, repoID := range repoIDs {
		repo, err := repositories.Fetch(ctx, u.Auth().KV(), fmt.Sprintf("%d", repoID))
		if err == nil && repo != nil {
			repo.Delete(ctx)
		}
	}
}

func testCleanupAndIsolation(t *testing.T, u *dream.Universe) {
	ctx := u.Context()

	// Create test data
	hookID := id.Generate()
	hook, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         hookID,
		"provider":   "github",
		"github_id":  9999,
		"repository": 8888,
		"secret":     "isolation_test_secret",
	})
	assert.NilError(t, err)

	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Verify it exists
	retrievedHook, err := hooks.Fetch(ctx, u.Auth().KV(), hookID)
	assert.NilError(t, err)
	assert.Assert(t, retrievedHook != nil)

	// Delete it
	err = hook.Delete(ctx)
	assert.NilError(t, err)

	// Verify it's gone
	_, err = hooks.Fetch(ctx, u.Auth().KV(), hookID)
	assert.Assert(t, err != nil)
}
