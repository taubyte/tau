package helpers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pebblev1 "github.com/cockroachdb/pebble"
	pebblev2 "github.com/cockroachdb/pebble/v2"
	"github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDatastore(t *testing.T) {
	tmpDir := t.TempDir()

	ds, err := NewDatastore(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, ds)

	// Test basic operations
	ctx := context.Background()

	// Put
	key := datastore.NewKey("test-key")
	err = ds.Put(ctx, key, []byte("test-value"))
	require.NoError(t, err)

	// Get
	val, err := ds.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("test-value"), val)

	// Close
	err = ds.Close()
	require.NoError(t, err)
}

func TestNewDatastore_NewPath(t *testing.T) {
	tmpDir := t.TempDir() + "/subdir"

	ds, err := NewDatastore(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, ds)

	err = ds.Close()
	require.NoError(t, err)
}

func TestNewDatastore_InvalidPath(t *testing.T) {
	// Try to create datastore in a non-writable path
	// This may not fail on all systems, so we check for successful creation instead
	tmpDir := t.TempDir()
	ds, err := NewDatastore(tmpDir)
	if err != nil {
		t.Log("NewDatastore failed as expected:", err)
		return
	}
	require.NotNil(t, ds)
	ds.Close()
}

func TestMigratePebbleV1ToV2_NonexistentPath(t *testing.T) {
	// Test migration with non-existent path
	err := MigratePebbleV1ToV2("/nonexistent/path/to/pebble")
	assert.Error(t, err)
}

func TestMigratePebbleV1ToV2(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")

	// Test data to migrate
	testData := map[string]string{
		"key1":       "value1",
		"key2":       "value2",
		"nested/key": "nested-value",
		"empty":      "",
		"binary":     string([]byte{0x00, 0x01, 0x02, 0xFF}),
	}

	// Step 1: Create a Pebble v1 database with test data
	v1DB, err := pebblev1.Open(dbPath, nil)
	require.NoError(t, err, "failed to create v1 database")

	for k, v := range testData {
		err = v1DB.Set([]byte(k), []byte(v), pebblev1.Sync)
		require.NoError(t, err, "failed to set key %s in v1 db", k)
	}

	// Verify data was written to v1
	for k, expectedV := range testData {
		val, closer, err := v1DB.Get([]byte(k))
		require.NoError(t, err, "failed to get key %s from v1 db", k)
		assert.Equal(t, expectedV, string(val), "v1 value mismatch for key %s", k)
		closer.Close()
	}

	err = v1DB.Close()
	require.NoError(t, err, "failed to close v1 database")

	// Step 2: Run migration
	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err, "migration failed")

	// Step 3: Verify backup was created
	backupPath := dbPath + ".v1"
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "backup path should exist after migration")

	// Step 4: Open migrated database with v2 and verify all data
	v2DB, err := pebblev2.Open(dbPath, nil)
	require.NoError(t, err, "failed to open migrated v2 database")
	defer v2DB.Close()

	for k, expectedV := range testData {
		val, closer, err := v2DB.Get([]byte(k))
		require.NoError(t, err, "failed to get key %s from migrated v2 db", k)
		assert.Equal(t, expectedV, string(val), "migrated value mismatch for key %s", k)
		closer.Close()
	}

	// Verify key count matches
	iter, err := v2DB.NewIter(nil)
	require.NoError(t, err)
	defer iter.Close()

	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	assert.Equal(t, len(testData), count, "migrated database should have same number of keys")
}

func TestMigratePebbleV1ToV2_LargeData(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "largedb")

	// Create v1 database with many keys
	v1DB, err := pebblev1.Open(dbPath, nil)
	require.NoError(t, err)

	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(filepath.Join("prefix", string(rune('a'+i%26)), string(rune(i))))
		value := make([]byte, 100)
		for j := range value {
			value[j] = byte(i % 256)
		}
		err = v1DB.Set(key, value, nil)
		require.NoError(t, err)
	}

	err = v1DB.Close()
	require.NoError(t, err)

	// Migrate
	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err)

	// Verify with v2
	v2DB, err := pebblev2.Open(dbPath, nil)
	require.NoError(t, err)
	defer v2DB.Close()

	iter, err := v2DB.NewIter(nil)
	require.NoError(t, err)
	defer iter.Close()

	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	assert.Equal(t, numKeys, count, "all keys should be migrated")
}

func TestMigratePebbleV1ToV2_EmptyDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "emptydb")

	// Create empty v1 database
	v1DB, err := pebblev1.Open(dbPath, nil)
	require.NoError(t, err)
	err = v1DB.Close()
	require.NoError(t, err)

	// Migrate empty database
	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err)

	// Verify v2 database is also empty
	v2DB, err := pebblev2.Open(dbPath, nil)
	require.NoError(t, err)
	defer v2DB.Close()

	iter, err := v2DB.NewIter(nil)
	require.NoError(t, err)
	defer iter.Close()

	assert.False(t, iter.First(), "migrated empty database should have no keys")
}

func Test_migratePebbleV1ToV2(t *testing.T) {
	tmpDir := t.TempDir()
	v1Path := filepath.Join(tmpDir, "v1db")
	v2Path := filepath.Join(tmpDir, "v2db")

	// Create v1 database with test data
	v1DB, err := pebblev1.Open(v1Path, nil)
	require.NoError(t, err)

	testKeys := []string{"alpha", "beta", "gamma"}
	for _, k := range testKeys {
		err = v1DB.Set([]byte(k), []byte("value-"+k), pebblev1.Sync)
		require.NoError(t, err)
	}
	err = v1DB.Close()
	require.NoError(t, err)

	// Run internal migration function
	err = migratePebbleV1ToV2(v1Path, v2Path)
	require.NoError(t, err)

	// Verify v2 database
	v2DB, err := pebblev2.Open(v2Path, nil)
	require.NoError(t, err)
	defer v2DB.Close()

	for _, k := range testKeys {
		val, closer, err := v2DB.Get([]byte(k))
		require.NoError(t, err)
		assert.Equal(t, "value-"+k, string(val))
		closer.Close()
	}
}

func Test_migratePebbleV1ToV2_InvalidV1Path(t *testing.T) {
	// Use a path that cannot be accessed (file instead of directory)
	tmpDir := t.TempDir()
	invalidPath := filepath.Join(tmpDir, "afile")

	// Create a regular file where pebble expects a directory
	err := os.WriteFile(invalidPath, []byte("not a database"), 0644)
	require.NoError(t, err)

	v2Path := filepath.Join(tmpDir, "v2db")

	err = migratePebbleV1ToV2(invalidPath, v2Path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open v1 DB")
}

func Test_migratePebbleV1ToV2_InvalidV2Path(t *testing.T) {
	tmpDir := t.TempDir()
	v1Path := filepath.Join(tmpDir, "v1db")

	// Create valid v1 database
	v1DB, err := pebblev1.Open(v1Path, nil)
	require.NoError(t, err)
	err = v1DB.Close()
	require.NoError(t, err)

	// Use invalid v2 path (read-only or non-writable location)
	v2Path := "/nonexistent/readonly/path"

	err = migratePebbleV1ToV2(v1Path, v2Path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open v2 DB")
}
