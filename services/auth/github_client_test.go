package auth

import (
	"context"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestGitHubClientCreation(t *testing.T) {
	t.Run("create GitHub client", func(t *testing.T) {
		client := &githubClient{}
		assert.Assert(t, client != nil)
	})

	t.Run("create auth service with GitHub support", func(t *testing.T) {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)
		mockFactory := mock.New()

		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			Node:        mockNode,
			Databases:   mockFactory,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12350"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12350"},
			PrivateKey:  []byte("private-key"),
			DomainValidation: config.DomainValidation{
				PrivateKey: []byte("private-key"),
				PublicKey:  []byte("public-key"),
			},
		}

		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		// Verify the service was created successfully
		assert.Assert(t, svc != nil)
		assert.Equal(t, svc.Node(), mockNode)
		assert.Assert(t, svc.KV() != nil)
	})
}

func TestGitHubResponseTypes(t *testing.T) {
	t.Run("repository registration response", func(t *testing.T) {
		response := RepositoryRegistrationResponse{
			Key: "test-key-123",
		}
		assert.Equal(t, response.Key, "test-key-123")
	})

	t.Run("project response", func(t *testing.T) {
		response := ProjectResponse{
			Project: ProjectInfo{
				ID:   "project-123",
				Name: "test-project",
			},
		}
		assert.Equal(t, response.Project.ID, "project-123")
		assert.Equal(t, response.Project.Name, "test-project")
	})

	t.Run("project create response", func(t *testing.T) {
		response := ProjectCreateResponse{
			Project: ProjectInfo{
				ID:   "new-project-123",
				Name: "new-project",
			},
		}
		assert.Equal(t, response.Project.ID, "new-project-123")
		assert.Equal(t, response.Project.Name, "new-project")
	})

	t.Run("user repositories response", func(t *testing.T) {
		response := UserRepositoriesResponse{
			Repositories: map[string]RepositoryInfo{
				"repo1": {
					ID:   "repo-1",
					Name: "test-repo-1",
				},
				"repo2": {
					ID:   "repo-2",
					Name: "test-repo-2",
				},
			},
		}
		assert.Equal(t, len(response.Repositories), 2)
		assert.Equal(t, response.Repositories["repo1"].Name, "test-repo-1")
		assert.Equal(t, response.Repositories["repo2"].Name, "test-repo-2")
	})

	t.Run("user projects response", func(t *testing.T) {
		response := UserProjectsResponse{
			Projects: []ProjectInfo{
				{
					ID:   "project-1",
					Name: "test-project-1",
				},
				{
					ID:   "project-2",
					Name: "test-project-2",
				},
			},
		}
		assert.Equal(t, len(response.Projects), 2)
		assert.Equal(t, response.Projects[0].Name, "test-project-1")
		assert.Equal(t, response.Projects[1].Name, "test-project-2")
	})

	t.Run("project details response", func(t *testing.T) {
		response := ProjectInfoResponse{
			Project: ProjectDetails{
				ID:   "project-123",
				Name: "test-project",
				Repositories: RepositoryDetails{
					Provider: "github",
					Configuration: RepositoryShortInfo{
						ID:   "config-repo",
						Name: "config",
					},
					Code: RepositoryShortInfo{
						ID:   "code-repo",
						Name: "code",
					},
				},
			},
		}
		assert.Equal(t, response.Project.ID, "project-123")
		assert.Equal(t, response.Project.Repositories.Provider, "github")
		assert.Equal(t, response.Project.Repositories.Configuration.ID, "config-repo")
		assert.Equal(t, response.Project.Repositories.Code.ID, "code-repo")
	})

	t.Run("project delete response", func(t *testing.T) {
		response := ProjectDeleteResponse{
			Project: ProjectDeleteInfo{
				ID:     "project-123",
				Status: "deleted",
			},
		}
		assert.Equal(t, response.Project.ID, "project-123")
		assert.Equal(t, response.Project.Status, "deleted")
	})

	t.Run("user response", func(t *testing.T) {
		response := UserResponse{
			User: UserInfo{
				Name:    "Test User",
				Company: "Test Company",
				Email:   "test@example.com",
				Login:   "testuser",
			},
		}
		assert.Equal(t, response.User.Name, "Test User")
		assert.Equal(t, response.User.Company, "Test Company")
		assert.Equal(t, response.User.Email, "test@example.com")
		assert.Equal(t, response.User.Login, "testuser")
	})
}

func TestMockDatabaseIntegration(t *testing.T) {
	t.Run("create mock database", func(t *testing.T) {
		mockFactory := mock.New()
		assert.Assert(t, mockFactory != nil)
	})

	t.Run("create mock peer node", func(t *testing.T) {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)
		assert.Assert(t, mockNode != nil)
	})
}

// Test GitHub client methods using gock to mock API responses
func TestGitHubClientMethodsWithGock(t *testing.T) {
	defer gock.Off()

	t.Run("test GetByID with invalid ID", func(t *testing.T) {
		ctx := context.Background()
		client := &githubClient{ctx: ctx}

		// Test GetByID with invalid ID (should fail parsing)
		err := client.GetByID("invalid-id")
		assert.Assert(t, err != nil, "Expected error for invalid ID")
	})

	t.Run("test CreateDeployKey without repository", func(t *testing.T) {
		ctx := context.Background()
		client := &githubClient{ctx: ctx}

		keyName := "test-key"
		keyContent := "ssh-rsa test-key-content"

		// Test CreateDeployKey without repository (should fail)
		err := client.CreateDeployKey(&keyName, &keyContent)
		assert.Assert(t, err != nil, "Expected error for no repository selected")
	})

	t.Run("test CreatePushHook without repository", func(t *testing.T) {
		ctx := context.Background()
		client := &githubClient{ctx: ctx}

		hookName := "test-hook"
		hookURL := "https://test.com/webhook"

		// Test CreatePushHook without repository (should fail)
		_, _, err := client.CreatePushHook(&hookName, &hookURL, false)
		assert.Assert(t, err != nil, "Expected error for no repository selected")
	})

	t.Run("test GetCurrentRepository without repository", func(t *testing.T) {
		ctx := context.Background()
		client := &githubClient{ctx: ctx}

		// Test GetCurrentRepository without repository (should fail)
		_, err := client.GetCurrentRepository()
		assert.Assert(t, err != nil, "Expected error for no current repository")
	})

	// Note: ShortRepositoryInfo requires a real GitHub client to test properly
	// We'll skip testing it for now since it requires complex mocking
}
