package repositories

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestGitHubRepository_Serialize(t *testing.T) {
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	data := repo.Serialize()

	assert.Equal(t, data["id"], 123)
	assert.Equal(t, data["provider"], "github")
	assert.Equal(t, data["project"], "test-project")
	assert.Equal(t, data["key"], "test-key-123")
}

func TestGitHubRepository_Register(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Verify the data was stored
	key, err := db.Get(ctx, "/repositories/github/123/key")
	assert.NilError(t, err)
	assert.Equal(t, string(key), "test-key-123")
}

func TestGitHubRepository_Delete(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	// First register the repository
	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Now delete it
	err = repo.Delete(ctx)
	assert.NilError(t, err)

	// Verify it no longer exists
	_, err = db.Get(ctx, "/repositories/github/123/key")
	assert.Assert(t, err != nil)
}

func TestExist(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent repository
	exists := Exist(ctx, db, "999")
	assert.Assert(t, !exists)

	// Create a repository
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test with existing repository
	exists = Exist(ctx, db, "123")
	assert.Assert(t, exists)
}

func TestExistOn(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent repository
	exists := ExistOn(ctx, db, "github", "999")
	assert.Assert(t, !exists)

	// Create a repository
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test with existing repository
	exists = ExistOn(ctx, db, "github", "123")
	assert.Assert(t, exists)
}

func TestProvider(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent repository
	_, err = Provider(ctx, db, "999")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")

	// Create a repository
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test with existing repository
	provider, err := Provider(ctx, db, "123")
	assert.NilError(t, err)
	assert.Equal(t, provider, "github")
}

func TestNew(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	data := Data{
		"id":       123,
		"provider": "github",
		"project":  "test-project",
		"key":      "test-key-123",
	}

	repo, err := New(db, data)
	assert.NilError(t, err)
	assert.Assert(t, repo != nil)

	// Verify the repository data
	assert.Equal(t, repo.ID(), 123)
	assert.Equal(t, repo.Provider(), "github")
}

func TestNew_WithMissingData(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with missing required data
	data := Data{
		"id": 123,
		// Missing provider, project, key
	}

	_, err = New(db, data)
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestNew_WithInvalidProvider(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with invalid provider
	data := Data{
		"id":       123,
		"provider": "invalid-provider",
		"project":  "test-project",
		"key":      "test-key-123",
	}

	_, err = New(db, data)
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestGitHubRepository_Hooks(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	// Test with no hooks
	hooks := repo.Hooks(ctx)
	assert.Assert(t, hooks != nil)
	assert.Equal(t, len(hooks), 0)

	// Add some hook data to test the regex matching
	err = db.Put(ctx, "/repositories/github/123/hooks/hook1", []byte("hook1-data"))
	assert.NilError(t, err)
	err = db.Put(ctx, "/repositories/github/123/hooks/hook2", []byte("hook2-data"))
	assert.NilError(t, err)

	// Test with hooks (note: this will fail to fetch actual hooks since we don't have the hooks package mocked)
	hooks = repo.Hooks(ctx)
	assert.Assert(t, hooks != nil)
	// The hooks will be empty because hooks.Fetch will fail, but the function should not panic
}

func TestFetchGithub(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent repository
	_, err = fetchGithub(ctx, db, 999)
	assert.Assert(t, err != nil)

	// Create a repository
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test with existing repository
	fetchedRepo, err := fetchGithub(ctx, db, 123)
	assert.NilError(t, err)
	assert.Assert(t, fetchedRepo != nil)

	// Verify the fetched data
	assert.Equal(t, fetchedRepo.ID(), 123)
	assert.Equal(t, fetchedRepo.Provider(), "github")
}

func TestFetch(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent repository
	_, err = Fetch(ctx, db, "999")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")

	// Create a repository
	repo := &githubRepository{
		repositoryCommon: repositoryCommon{
			kv:       db,
			provider: "github",
			project:  "test-project",
		},
		id:  123,
		key: "test-key-123",
	}

	err = repo.Register(ctx)
	assert.NilError(t, err)

	// Test with existing repository
	fetchedRepo, err := Fetch(ctx, db, "123")
	assert.NilError(t, err)
	assert.Assert(t, fetchedRepo != nil)

	// Verify the fetched data
	assert.Equal(t, fetchedRepo.ID(), 123)
	assert.Equal(t, fetchedRepo.Provider(), "github")
}

func TestFetch_InvalidID(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with invalid ID format
	_, err = Fetch(ctx, db, "invalid-id")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestFetch_UnsupportedProvider(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Store a repository with unsupported provider
	err = db.Put(ctx, "/repositories/unsupported/123/key", []byte("test-key"))
	assert.NilError(t, err)

	// Try to fetch repository with unsupported provider
	_, err = Fetch(ctx, db, "123")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestGitProviders(t *testing.T) {
	// Test that GitProviders contains expected values
	assert.Assert(t, len(GitProviders) > 0)

	// Check that github is included
	hasGithub := false
	for _, provider := range GitProviders {
		if provider == "github" {
			hasGithub = true
			break
		}
	}
	assert.Assert(t, hasGithub, "GitProviders should contain 'github'")
}

// Note: Repository interface doesn't expose Project() or Key() methods
// These fields are internal to the implementation
