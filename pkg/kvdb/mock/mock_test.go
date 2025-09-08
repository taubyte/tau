package mock

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"gotest.tools/v3/assert"
)

func TestMockKVDB(t *testing.T) {
	logger := log.Logger("test")

	t.Run("New", func(t *testing.T) {
		factory := New()
		assert.Assert(t, factory != nil)
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		assert.Assert(t, db != nil)
		defer db.Close()
	})

	t.Run("Put and Get", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()
		key := "test-key"
		value := []byte("test-value")

		// Test Put
		err = db.Put(ctx, key, value)
		assert.NilError(t, err)

		// Test Get
		retrieved, err := db.Get(ctx, key)
		assert.NilError(t, err)
		assert.DeepEqual(t, retrieved, value)
	})

	t.Run("Delete", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()
		key := "test-key"
		value := []byte("test-value")

		// Put a value first
		err = db.Put(ctx, key, value)
		assert.NilError(t, err)

		// Verify it exists
		retrieved, err := db.Get(ctx, key)
		assert.NilError(t, err)
		assert.DeepEqual(t, retrieved, value)

		// Delete it
		err = db.Delete(ctx, key)
		assert.NilError(t, err)

		// Verify it's gone
		_, err = db.Get(ctx, key)
		assert.Assert(t, err != nil, "Expected error after deletion")
	})

	t.Run("List", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Put some test data
		testData := map[string][]byte{
			"prefix1/key1": []byte("value1"),
			"prefix1/key2": []byte("value2"),
			"prefix2/key3": []byte("value3"),
			"key4":         []byte("value4"),
		}

		for key, value := range testData {
			err = db.Put(ctx, key, value)
			assert.NilError(t, err)
		}

		// Test listing all keys
		allKeys, err := db.List(ctx, "")
		assert.NilError(t, err)
		assert.Equal(t, len(allKeys), 4)

		// Test listing with prefix
		prefix1Keys, err := db.List(ctx, "prefix1/")
		assert.NilError(t, err)
		assert.Equal(t, len(prefix1Keys), 2)

		// Test listing with non-existent prefix
		noKeys, err := db.List(ctx, "nonexistent/")
		assert.NilError(t, err)
		assert.Equal(t, len(noKeys), 0)
	})

	t.Run("ListAsync", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Put some test data
		testData := map[string][]byte{
			"async1/key1": []byte("value1"),
			"async1/key2": []byte("value2"),
			"async2/key3": []byte("value3"),
		}

		for key, value := range testData {
			err = db.Put(ctx, key, value)
			assert.NilError(t, err)
		}

		// Test async listing
		ch, err := db.ListAsync(ctx, "async1/")
		assert.NilError(t, err)

		var keys []string
		for key := range ch {
			keys = append(keys, key)
		}

		assert.Equal(t, len(keys), 2)
	})

	t.Run("ListRegEx", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Put some test data
		testData := map[string][]byte{
			"regex1/key1": []byte("value1"),
			"regex1/key2": []byte("value2"),
			"regex2/key3": []byte("value3"),
			"regex1/key4": []byte("value4"),
		}

		for key, value := range testData {
			err = db.Put(ctx, key, value)
			assert.NilError(t, err)
		}

		// Test regex listing
		keys, err := db.ListRegEx(ctx, "regex1/", ".*key[12]$")
		assert.NilError(t, err)
		assert.Equal(t, len(keys), 2)

		// Test regex with prefix
		keys, err = db.ListRegEx(ctx, "", "regex1/.*")
		assert.NilError(t, err)
		assert.Equal(t, len(keys), 3)
	})

	t.Run("ListRegExAsync", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Put some test data
		testData := map[string][]byte{
			"regexasync1/key1": []byte("value1"),
			"regexasync1/key2": []byte("value2"),
			"regexasync2/key3": []byte("value3"),
		}

		for key, value := range testData {
			err = db.Put(ctx, key, value)
			assert.NilError(t, err)
		}

		// Test async regex listing
		ch, err := db.ListRegExAsync(ctx, "regexasync1/", ".*key[12]$")
		assert.NilError(t, err)

		var keys []string
		for key := range ch {
			keys = append(keys, key)
		}

		assert.Equal(t, len(keys), 2)
	})

	t.Run("Batch", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Create a batch
		batch, err := db.Batch(ctx)
		assert.NilError(t, err)

		// Add operations to batch
		err = batch.Put("batch-key1", []byte("batch-value1"))
		assert.NilError(t, err)

		err = batch.Put("batch-key2", []byte("batch-value2"))
		assert.NilError(t, err)

		err = batch.Delete("batch-key1")
		assert.NilError(t, err)

		// Commit the batch
		err = batch.Commit()
		assert.NilError(t, err)

		// Verify results
		_, err = db.Get(ctx, "batch-key1")
		assert.Assert(t, err != nil, "Expected error for deleted key")

		value, err := db.Get(ctx, "batch-key2")
		assert.NilError(t, err)
		assert.DeepEqual(t, value, []byte("batch-value2"))
	})

	t.Run("Sync", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		// Sync should not error
		err = db.Sync(ctx, "test-key")
		assert.NilError(t, err)
	})

	t.Run("Stats", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		ctx := context.Background()

		stats := db.Stats(ctx)
		assert.Assert(t, stats != nil)
		assert.Equal(t, uint(0), uint(stats.Type())) // TypeCRDT = 0
		assert.Equal(t, len(stats.Heads()), 0)

		// Test encode/decode (mock implementations)
		encoded := stats.Encode()
		assert.Equal(t, len(encoded), 0)

		err = stats.Decode([]byte("test"))
		assert.NilError(t, err)
	})

	t.Run("Factory", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		retrievedFactory := db.Factory()
		assert.Assert(t, retrievedFactory != nil)

		// Test creating another database through factory
		db2, err := retrievedFactory.New(logger, "test2", 5)
		assert.NilError(t, err)
		assert.Assert(t, db2 != nil)
		defer db2.Close()

		// Test that they're different instances
		assert.Assert(t, db != db2)
	})

	t.Run("Close", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)

		// Put some data
		ctx := context.Background()
		err = db.Put(ctx, "test-key", []byte("test-value"))
		assert.NilError(t, err)

		// Close the database
		db.Close()

		// Try to use closed database
		_, err = db.Get(ctx, "test-key")
		assert.Assert(t, err != nil, "Expected error from closed database")
	})

	t.Run("Context cancellation", func(t *testing.T) {
		factory := New()
		defer factory.Close()

		db, err := factory.New(logger, "test", 5)
		assert.NilError(t, err)
		defer db.Close()

		// Put some data
		ctx := context.Background()
		err = db.Put(ctx, "test-key", []byte("test-value"))
		assert.NilError(t, err)

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		// Test async operations with context cancellation
		ch, err := db.ListAsync(ctx, "")
		assert.NilError(t, err)

		// Wait for context to be cancelled
		time.Sleep(20 * time.Millisecond)

		// Channel should be closed due to context cancellation
		// Note: The channel might still have buffered data, so we need to read it all
		closed := false
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					closed = true
					goto done
				}
			case <-time.After(50 * time.Millisecond):
				goto done
			}
		}
	done:
		assert.Assert(t, closed, "Channel should be closed after context cancellation")
	})
}
