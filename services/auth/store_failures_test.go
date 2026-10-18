package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// errStoreFailure is what every fixture in this file returns instead of
// delegating to the real Store underneath it — a real error from a real
// store, the same interface any actual backend implements, injected at
// exactly the seam this package's own error handling exists to catch. None
// of it narrows or skips a check in the code under test; it exists so the
// checks that are already there have something to catch.
var errStoreFailure = errors.New("deliberate store failure")

// failingKVDB wraps a real Store and fails whichever of Put / Delete /
// List / ListRegEx / Batch is configured to fail, delegating everything
// else to the real store underneath.
type failingKVDB struct {
	Store
	failPut, failDelete, failListRegEx, failBatch bool
}

func (k *failingKVDB) Put(ctx context.Context, key string, v []byte) error {
	if k.failPut {
		return errStoreFailure
	}
	return k.Store.Put(ctx, key, v)
}

func (k *failingKVDB) Delete(ctx context.Context, key string) error {
	if k.failDelete {
		return errStoreFailure
	}
	return k.Store.Delete(ctx, key)
}

func (k *failingKVDB) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {
	if k.failListRegEx {
		return nil, errStoreFailure
	}
	return k.Store.ListRegEx(ctx, prefix, regexs...)
}

func (k *failingKVDB) Batch(ctx context.Context) (kvdb.Batch, error) {
	if k.failBatch {
		return nil, errStoreFailure
	}
	return k.Store.Batch(ctx)
}

// failingBatch wraps a real kvdb.Batch and fails its Nth Put-or-Delete call
// (1-indexed), delegating every other call to the real batch so the
// operations around the failure still take effect the way they would in
// production up to the point something actually went wrong.
type failingBatch struct {
	real   kvdb.Batch
	failAt int
	n      int
}

func (b *failingBatch) Put(key string, value []byte) error {
	b.n++
	if b.n == b.failAt {
		return errStoreFailure
	}
	return b.real.Put(key, value)
}

func (b *failingBatch) Delete(key string) error {
	b.n++
	if b.n == b.failAt {
		return errStoreFailure
	}
	return b.real.Delete(key)
}

func (b *failingBatch) Commit() error { return b.real.Commit() }

// batchFailingKVDB wraps a real Store and returns a failingBatch from
// Batch(), so a store function's Nth batched write — not just batch
// creation itself — can be made to fail.
type batchFailingKVDB struct {
	Store
	failAt int
}

func (k *batchFailingKVDB) Batch(ctx context.Context) (kvdb.Batch, error) {
	real, err := k.Store.Batch(ctx)
	if err != nil {
		return nil, err
	}
	return &failingBatch{real: real, failAt: k.failAt}, nil
}

// putFailsAfter wraps a real Store and fails the failOn-th direct Put
// call (1-indexed), succeeding on every other one — for reaching a SECOND
// write's own error branch in code that makes two structurally identical
// Put calls in a row (projects.go's Create links two repositories the same
// way), where the first has to succeed for the second to even run.
type putFailsAfter struct {
	Store
	n, failOn int
}

func (k *putFailsAfter) Put(ctx context.Context, key string, v []byte) error {
	k.n++
	if k.n == k.failOn {
		return errStoreFailure
	}
	return k.Store.Put(ctx, key, v)
}

func TestRegisterRepository_BatchCreationFails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failBatch: true}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	err = registerRepository(t.Context(), kv, key, "QmOwner")
	assert.Error(t, err)
}

func TestRegisterRepository_BatchedWriteFails(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		key, err := parseRepoKey("github.com/acme/pay")
		require.NoError(t, err)
		kv := &batchFailingKVDB{Store: newStore(t), failAt: failAt}

		err = registerRepository(t.Context(), kv, key, "QmOwner")
		assert.Error(t, err, "failAt=%d", failAt)
	}
}

func TestDeleteRepository_ListHooksFails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failListRegEx: true}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	err = deleteRepository(t.Context(), kv, key)
	assert.Error(t, err)
}

func TestDeleteRepository_BatchCreationFails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failBatch: true}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	err = deleteRepository(t.Context(), kv, key)
	assert.Error(t, err)
}

func TestDeleteRepository_BatchedWriteFails(t *testing.T) {
	// Three fixed deletes (key, registered_by, project) plus one hook's
	// worth of rows (repository, handle, scheme, secret, reverse index) —
	// every position is exercised so every `if err :=
	// batch.Delete(...); err != nil` line in deleteRepository and
	// batchDeleteHook is reached, not just the first.
	for failAt := 1; failAt <= 7; failAt++ {
		real := newStore(t)
		key, err := parseRepoKey("github.com/acme/pay")
		require.NoError(t, err)
		require.NoError(t, registerRepository(t.Context(), real, key, "QmOwner"))
		require.NoError(t, putHook(t.Context(), real, "hook-1", hook(key.String())))
		require.NoError(t, real.Put(t.Context(), repoHookRow(key, "hook-1"), nil))

		kv := &batchFailingKVDB{Store: real, failAt: failAt}
		err = deleteRepository(t.Context(), kv, key)
		assert.Error(t, err, "failAt=%d", failAt)
	}
}

func TestBatchDeleteHook_EachOperationFails(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		real := newStore(t)
		realBatch, err := real.Batch(t.Context())
		require.NoError(t, err)

		err = batchDeleteHook(&failingBatch{real: realBatch, failAt: failAt}, "hook-1")
		assert.Error(t, err, "failAt=%d", failAt)
	}
}

func TestPutProject_BatchCreationFails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failBatch: true}
	config, err := parseRepoKey("github.com/acme/config")
	require.NoError(t, err)
	code, err := parseRepoKey("github.com/acme/code")
	require.NoError(t, err)

	err = putProject(t.Context(), kv, "proj-1", "demo", config, code)
	assert.Error(t, err)
}

func TestPutProject_BatchedWriteFails(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		kv := &batchFailingKVDB{Store: newStore(t), failAt: failAt}
		config, err := parseRepoKey("github.com/acme/config")
		require.NoError(t, err)
		code, err := parseRepoKey("github.com/acme/code")
		require.NoError(t, err)

		err = putProject(t.Context(), kv, "proj-1", "demo", config, code)
		assert.Error(t, err, "failAt=%d", failAt)
	}
}

func TestLinkRepositoryToProject_Fails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failPut: true}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	err = linkRepositoryToProject(t.Context(), kv, key, "proj-1")
	assert.Error(t, err)
}

func TestListRepositoryKeys_Fails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failListRegEx: true}
	_, err := listRepositoryKeys(t.Context(), kv)
	assert.Error(t, err)
}

func TestListProjectIDs_Fails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failListRegEx: true}
	_, err := listProjectIDs(t.Context(), kv)
	assert.Error(t, err)
}

func TestPutHook_Fails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failBatch: true}
	assert.Error(t, putHook(t.Context(), kv, "hook-1", hook("github.com/acme/pay")))

	for failAt := 1; failAt <= 4; failAt++ {
		kv := &batchFailingKVDB{Store: newStore(t), failAt: failAt}
		assert.Error(t, putHook(t.Context(), kv, "hook-1", hook("github.com/acme/pay")), "failAt=%d", failAt)
	}
}

func TestListHookIDs_Fails(t *testing.T) {
	kv := &failingKVDB{Store: newStore(t), failListRegEx: true}
	_, err := listHookIDs(t.Context(), kv)
	assert.Error(t, err)
}
