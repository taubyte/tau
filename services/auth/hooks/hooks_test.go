package hooks

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestHookCommon_Register(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	hook := &HookCommon{
		KV:       db,
		Id:       "test-hook-123",
		Provider: "github",
	}

	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Verify the data was stored
	provider, err := db.Get(ctx, "/hooks/test-hook-123/provider")
	assert.NilError(t, err)
	assert.Equal(t, string(provider), "github")
}

func TestHookCommon_Delete(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	hook := &HookCommon{
		KV:       db,
		Id:       "test-hook-123",
		Provider: "github",
	}

	// First register the hook
	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Now delete it
	err = hook.Delete(ctx)
	assert.NilError(t, err)

	// Verify it no longer exists
	_, err = db.Get(ctx, "/hooks/test-hook-123/provider")
	assert.Assert(t, err != nil)
}

func TestHookCommon_ID(t *testing.T) {
	hook := &HookCommon{
		Id: "test-hook-123",
	}

	id := hook.ID()
	assert.Equal(t, id, "test-hook-123")
}

func TestGithubHook_Serialize(t *testing.T) {
	hook := &GithubHook{
		HookCommon: HookCommon{
			Id:       "test-hook-123",
			Provider: "github",
		},
		GithubId:   456,
		Secret:     "test-secret",
		Repository: 789,
	}

	data := hook.Serialize()

	assert.Equal(t, data["id"], "test-hook-123")
	assert.Equal(t, data["provider"], "github")
	assert.Equal(t, data["github_id"], 456)
	assert.Equal(t, data["secret"], "test-secret")
	assert.Equal(t, data["repository"], 789)
}

func TestGithubHook_ProviderID(t *testing.T) {
	hook := &GithubHook{
		GithubId: 456,
	}

	providerID := hook.ProviderID()
	assert.Equal(t, providerID, "456")
}

func TestGithubHook_Delete(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	hook := &GithubHook{
		HookCommon: HookCommon{
			KV:       db,
			Id:       "test-hook-123",
			Provider: "github",
		},
		GithubId:   456,
		Secret:     "test-secret",
		Repository: 789,
	}

	// First register the hook
	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Now delete it
	err = hook.Delete(ctx)
	assert.NilError(t, err)

	// Verify it no longer exists
	_, err = db.Get(ctx, "/hooks/test-hook-123/provider")
	assert.Assert(t, err != nil)
}

func TestGithubHook_Register(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	hook := &GithubHook{
		HookCommon: HookCommon{
			KV:       db,
			Id:       "test-hook-123",
			Provider: "github",
		},
		GithubId:   456,
		Secret:     "test-secret",
		Repository: 789,
	}

	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Verify the data was stored
	provider, err := db.Get(ctx, "/hooks/test-hook-123/provider")
	assert.NilError(t, err)
	assert.Equal(t, string(provider), "github")

	githubID, err := db.Get(ctx, "/hooks/test-hook-123/github/id")
	assert.NilError(t, err)
	assert.Assert(t, len(githubID) > 0)

	secret, err := db.Get(ctx, "/hooks/test-hook-123/github/secret")
	assert.NilError(t, err)
	assert.Equal(t, string(secret), "test-secret")

	repository, err := db.Get(ctx, "/hooks/test-hook-123/github/repository")
	assert.NilError(t, err)
	assert.Assert(t, len(repository) > 0)

	// Verify repository hook reference
	_, err = db.Get(ctx, "/repositories/github/789/hooks/test-hook-123")
	assert.NilError(t, err)
	// The repository hook reference is stored as nil, so we just check there's no error
}

func TestExist(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent hook
	exists := Exist(ctx, db, "non-existent-hook")
	assert.Assert(t, !exists)

	// Create a hook
	hook := &HookCommon{
		KV:       db,
		Id:       "test-hook-123",
		Provider: "github",
	}

	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Test with existing hook
	exists = Exist(ctx, db, "test-hook-123")
	assert.Assert(t, exists)
}

func TestFetch(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test fetching non-existent hook
	_, err = Fetch(ctx, db, "non-existent-hook")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")

	// Create a hook
	hook := &GithubHook{
		HookCommon: HookCommon{
			KV:       db,
			Id:       "test-hook-123",
			Provider: "github",
		},
		GithubId:   456,
		Secret:     "test-secret",
		Repository: 789,
	}

	err = hook.Register(ctx)
	assert.NilError(t, err)

	// Fetch the hook
	fetchedHook, err := Fetch(ctx, db, "test-hook-123")
	assert.NilError(t, err)
	assert.Assert(t, fetchedHook != nil)

	// Verify the fetched data
	assert.Equal(t, fetchedHook.ID(), "test-hook-123")
	assert.Equal(t, fetchedHook.ProviderID(), "456")
}

func TestFetch_InvalidProvider(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Store a hook with invalid provider
	err = db.Put(ctx, "/hooks/invalid-hook/provider", []byte("invalid-provider"))
	assert.NilError(t, err)

	// Try to fetch hook with invalid provider
	_, err = Fetch(ctx, db, "invalid-hook")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestNew(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	data := Data{
		"id":         "test-hook-123",
		"provider":   "github",
		"github_id":  456,
		"secret":     "test-secret",
		"repository": 789,
	}

	hook, err := New(db, data)
	assert.NilError(t, err)
	assert.Assert(t, hook != nil)

	// Verify the hook data
	assert.Equal(t, hook.ID(), "test-hook-123")
	assert.Equal(t, hook.ProviderID(), "456")
}

func TestNew_WithMissingData(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with missing required data
	data := Data{
		"id": "test-hook-123",
		// Missing provider
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
		"id":       "test-hook-123",
		"provider": "invalid-provider",
	}

	_, err = New(db, data)
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}
