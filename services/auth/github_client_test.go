package auth

import (
	"context"
	"testing"

	"github.com/google/go-github/v71/github"
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
			Root:        t.TempDir(),
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

// Test GitHub client error handling and edge cases
func TestGitHubClientErrorHandling(t *testing.T) {
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

// Test GitHub client methods for comprehensive coverage
func TestGitHubClientComprehensiveCoverage(t *testing.T) {
	defer gock.Off()
	ctx := context.Background()

	// Test CreateRepository with gock
	t.Run("CreateRepository", func(t *testing.T) {
		// Mock GitHub API response for repository creation
		gock.New("https://api.github.com").
			Post("/user/repos").
			Reply(201).
			JSON(map[string]interface{}{
				"id":        12345,
				"name":      "test-repo",
				"full_name": "testuser/test-repo",
				"private":   true,
			})

		// Create a real GitHub client that gock can intercept
		ghClient := github.NewClient(nil) // nil transport will use gock
		client := &githubClient{
			Client: ghClient,
			ctx:    ctx,
		}

		// Test with valid parameters
		name := "test-repo"
		description := "Test repository"
		private := true

		err := client.CreateRepository(&name, &description, &private)
		assert.NilError(t, err)
		assert.Assert(t, client.current_repository != nil)
		assert.Equal(t, client.current_repository.GetName(), "test-repo")

		assert.Assert(t, gock.IsDone())
	})

	// Test CreateDeployKey with gock
	t.Run("CreateDeployKey", func(t *testing.T) {
		// Mock GitHub API response for deploy key creation
		gock.New("https://api.github.com").
			Post("/repos/testuser/test-repo/keys").
			Reply(201).
			JSON(map[string]interface{}{
				"id":    12345,
				"key":   "ssh-rsa test-key-content",
				"title": "test-key",
			})

		// Create a real GitHub client that gock can intercept
		ghClient := github.NewClient(nil) // nil transport will use gock
		client := &githubClient{
			Client: ghClient,
			ctx:    ctx,
		}

		// Test with nil repository (error path)
		name := "test-key"
		key := "test-key-content"
		err := client.CreateDeployKey(&name, &key)
		assert.Assert(t, err != nil, "Expected error for no repository selected")
		assert.Equal(t, err.Error(), "no repository selected")

		// Test with valid repository
		client.current_repository = &github.Repository{
			Name: github.Ptr("test-repo"),
		}
		client.user = &github.User{
			Login: github.Ptr("testuser"),
		}

		err = client.CreateDeployKey(&name, &key)
		assert.NilError(t, err)

		assert.Assert(t, gock.IsDone())
	})

	// Test CreatePushHook with gock
	t.Run("CreatePushHook", func(t *testing.T) {
		// Mock GitHub API response for webhook creation
		gock.New("https://api.github.com").
			Post("/repos/testuser/test-repo/hooks").
			Reply(201).
			JSON(map[string]interface{}{
				"id":     12345,
				"name":   "web",
				"active": true,
				"config": map[string]interface{}{
					"url": "https://test.com/webhook",
				},
			})

		// Create a real GitHub client that gock can intercept
		ghClient := github.NewClient(nil) // nil transport will use gock
		client := &githubClient{
			Client: ghClient,
			ctx:    ctx,
		}

		// Test with nil repository (error path)
		name := "test-hook"
		url := "https://test.com/webhook"
		_, _, err := client.CreatePushHook(&name, &url, false)
		assert.Assert(t, err != nil, "Expected error for no repository selected")
		assert.Equal(t, err.Error(), "no repository selected")

		// Set repository for dev mode test
		client.current_repository = &github.Repository{
			Name: github.Ptr("test-repo"),
		}
		client.user = &github.User{
			Login: github.Ptr("testuser"),
		}

		// Test devMode = true (should return success without API call)
		_, secret, err := client.CreatePushHook(&name, &url, true)
		assert.NilError(t, err)
		assert.Assert(t, secret != "")

		// Now test with repository set (this will use the gock mock)
		_, _, err = client.CreatePushHook(&name, &url, false)
		assert.NilError(t, err)

		assert.Assert(t, gock.IsDone())
	})

	// Test ListMyRepos with gock
	t.Run("ListMyRepos", func(t *testing.T) {
		// Mock GitHub API response for user repositories
		gock.New("https://api.github.com").
			Get("/user/repos").
			Reply(200).
			JSON([]map[string]interface{}{
				{
					"id":        111,
					"name":      "repo1",
					"full_name": "testuser/repo1",
					"url":       "https://api.github.com/repos/testuser/repo1",
				},
				{
					"id":        222,
					"name":      "repo2",
					"full_name": "testuser/repo2",
					"url":       "https://api.github.com/repos/testuser/repo2",
				},
			})

		// Create a real GitHub client that gock can intercept
		ghClient := github.NewClient(nil) // nil transport will use gock
		client := &githubClient{
			Client: ghClient,
			ctx:    ctx,
		}

		repos := client.ListMyRepos()
		assert.Assert(t, repos != nil)
		assert.Equal(t, len(repos), 2)

		assert.Assert(t, gock.IsDone())
	})

	// Test ShortRepositoryInfo with gock
	t.Run("ShortRepositoryInfo", func(t *testing.T) {
		// Mock GitHub API response for repository by ID
		gock.New("https://api.github.com").
			Get("/repositories/12345").
			Reply(200).
			JSON(map[string]interface{}{
				"id":        12345,
				"name":      "test-repo",
				"full_name": "testuser/test-repo",
				"url":       "https://api.github.com/repos/testuser/test-repo",
			})

		// Create a real GitHub client that gock can intercept
		ghClient := github.NewClient(nil) // nil transport will use gock
		client := &githubClient{
			Client: ghClient,
			ctx:    ctx,
		}

		// Test with invalid ID
		info := client.ShortRepositoryInfo("invalid-id")
		assert.Equal(t, info.Error, "Incorrect repository ID")

		// Test with valid ID
		info = client.ShortRepositoryInfo("12345")
		assert.Equal(t, info.ID, "12345")
		assert.Equal(t, info.Name, "test-repo")
		assert.Equal(t, info.FullName, "testuser/test-repo")

		assert.Assert(t, gock.IsDone())
	})
}
