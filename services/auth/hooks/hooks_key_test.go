package hooks

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestNewGithub(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	key, err := repokey.Parse("https://github.com/acme/pay")
	assert.NilError(t, err)

	hook := NewGithub(db, "test-hook-123", 456, "test-secret", key)

	assert.Equal(t, hook.ID(), "test-hook-123")
	assert.Equal(t, hook.ProviderID(), "456")
	assert.Equal(t, hook.Serialize()["repository"], "github.com/acme/pay")
}

// The reverse index is what lets unregister find the hooks it has to tear down,
// and losing it is SILENT — an empty hook list reads exactly like a repository
// with no hooks, and the webhook and deploy key stay behind on the provider.
// This pins the whole round trip for a subgroup key, whose fourth segment is
// the shape most likely to be mislaid.
func TestReverseIndexRoundTripsForASubgroupKey(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	key, err := repokey.Parse("https://gitlab.com/acme/team/widget")
	assert.NilError(t, err)

	hook := NewGithub(db, "hook-sub", 99, "s3cret", key)
	assert.NilError(t, hook.Register(ctx))

	_, err = db.Get(ctx, "/repositories/gitlab.com/acme/team/widget/hooks/hook-sub")
	assert.NilError(t, err)

	// The repository is stored as the key itself, not as a fixed-width integer,
	// so this also pins the value encoding that moved with the path.
	fetched, err := Fetch(ctx, db, "hook-sub")
	assert.NilError(t, err)
	assert.Equal(t, fetched.Serialize()["repository"], "gitlab.com/acme/team/widget")
	assert.Equal(t, fetched.Serialize()["secret"], "s3cret")
	assert.Equal(t, fetched.ProviderID(), "99")

	assert.NilError(t, hook.Delete(ctx))
	_, err = db.Get(ctx, "/repositories/gitlab.com/acme/team/widget/hooks/hook-sub")
	assert.Assert(t, err != nil)
}
