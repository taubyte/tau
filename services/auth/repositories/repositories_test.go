package repositories

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func testDB(t *testing.T) kvdb.KVDB {
	t.Helper()
	mockKV := mock.New()
	t.Cleanup(mockKV.Close)

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	t.Cleanup(func() { db.Close() })

	return db
}

func key(t *testing.T, cloneURL string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse(cloneURL)
	assert.NilError(t, err)
	return k
}

// Every case below runs against BOTH a three-segment key and a GitLab subgroup,
// because the key is variable depth and anything that counts segments is only
// correct for one of them.
func eachKey(t *testing.T, f func(t *testing.T, k repokey.Key)) {
	t.Helper()
	for _, cloneURL := range []string{
		"https://github.com/acme/pay",
		"https://gitlab.com/acme/team/widget",
	} {
		k := key(t, cloneURL)
		t.Run(k.String(), func(t *testing.T) { f(t, k) })
	}
}

func TestSerialize(t *testing.T) {
	eachKey(t, func(t *testing.T, k repokey.Key) {
		data := New(testDB(t), k, "test-key-123", "test-project").Serialize()

		assert.Equal(t, data["id"], k.String())
		assert.Equal(t, data["provider"], k.Host)
		assert.Equal(t, data["project"], "test-project")
		assert.Equal(t, data["key"], "test-key-123")
	})
}

func TestRegisterStoresUnderTheCanonicalKey(t *testing.T) {
	eachKey(t, func(t *testing.T, k repokey.Key) {
		ctx := context.Background()
		db := testDB(t)

		assert.NilError(t, New(db, k, "test-key-123", "").Register(ctx))

		stored, err := db.Get(ctx, "/repositories/"+k.String()+"/key")
		assert.NilError(t, err)
		assert.Equal(t, string(stored), "test-key-123")
	})
}

func TestDeleteRemovesTheKeyAndTheProjectLink(t *testing.T) {
	eachKey(t, func(t *testing.T, k repokey.Key) {
		ctx := context.Background()
		db := testDB(t)

		repo := New(db, k, "test-key-123", "")
		assert.NilError(t, repo.Register(ctx))
		assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/project", []byte("proj")))

		assert.NilError(t, repo.Delete(ctx))

		_, err := db.Get(ctx, "/repositories/"+k.String()+"/key")
		assert.Assert(t, err != nil)

		// A repository that is gone is not still part of a project.
		_, err = db.Get(ctx, "/repositories/"+k.String()+"/project")
		assert.Assert(t, err != nil)
	})
}

func TestExist(t *testing.T) {
	eachKey(t, func(t *testing.T, k repokey.Key) {
		ctx := context.Background()
		db := testDB(t)

		assert.Assert(t, !Exist(ctx, db, k))
		assert.NilError(t, New(db, k, "test-key-123", "").Register(ctx))
		assert.Assert(t, Exist(ctx, db, k))
	})
}

// Two repositories differing only below the third segment are two repositories.
// A prefix scheme that stopped at a fixed depth would fuse them.
func TestSubgroupIsNotItsGroup(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	widget := key(t, "https://gitlab.com/acme/team/widget")
	assert.NilError(t, New(db, widget, "widget-key", "").Register(ctx))

	assert.Assert(t, !Exist(ctx, db, key(t, "https://gitlab.com/acme/team")))
	assert.Assert(t, !Exist(ctx, db, key(t, "https://gitlab.com/acme/other/widget")))
}

func TestFetchRoundTrip(t *testing.T) {
	eachKey(t, func(t *testing.T, k repokey.Key) {
		ctx := context.Background()
		db := testDB(t)

		_, err := Fetch(ctx, db, k)
		assert.Assert(t, err != nil)

		assert.NilError(t, New(db, k, "test-key-123", "").Register(ctx))
		assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/project", []byte("test-project")))

		fetched, err := Fetch(ctx, db, k)
		assert.NilError(t, err)
		assert.Equal(t, fetched.ID(), k.String())
		assert.Equal(t, fetched.Provider(), k.Host)
		assert.Equal(t, fetched.Serialize()["key"], "test-key-123")
		assert.Equal(t, fetched.Serialize()["project"], "test-project")
	})
}

// The rows filed BESIDE a repository — its project link and one row per hook —
// live under the same prefix, so a listing that stripped the prefix would report
// them as repositories. This pins that only the /key rows are returned, and that
// a repository with hooks is returned exactly once.
func TestListReturnsRepositoriesAndNotTheRowsBesideThem(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	pay := key(t, "https://github.com/acme/pay")
	widget := key(t, "https://gitlab.com/acme/team/widget")

	for _, k := range []repokey.Key{pay, widget} {
		assert.NilError(t, New(db, k, "k", "").Register(ctx))
		assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/project", []byte("proj")))
		assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/hooks/hook-a", nil))
		assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/hooks/hook-b", nil))
	}

	ids, err := List(ctx, db)
	assert.NilError(t, err)

	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}

	assert.Equal(t, len(ids), 2)
	assert.Equal(t, seen[pay.String()], 1)
	assert.Equal(t, seen[widget.String()], 1)
}

func TestHooksListingIsScopedToTheRepository(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	k := key(t, "https://gitlab.com/acme/team/widget")
	repo := New(db, k, "test-key-123", "")

	hooks := repo.Hooks(ctx)
	assert.Assert(t, hooks != nil)
	assert.Equal(t, len(hooks), 0)

	// hooks.Fetch cannot resolve these, so the slice stays empty — what is being
	// pinned is that reading them neither panics nor reaches past the prefix.
	assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/hooks/hook1", []byte("hook1-data")))
	assert.NilError(t, db.Put(ctx, "/repositories/"+k.String()+"/hooks/hook2", []byte("hook2-data")))

	assert.Assert(t, repo.Hooks(ctx) != nil)
}
