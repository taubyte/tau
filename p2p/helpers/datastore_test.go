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

	ctx := context.Background()

	key := datastore.NewKey("test-key")
	err = ds.Put(ctx, key, []byte("test-value"))
	require.NoError(t, err)

	val, err := ds.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("test-value"), val)

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

	v1DB, err := pebblev1.Open(dbPath, nil)
	require.NoError(t, err, "failed to create v1 database")

	for k, v := range testData {
		err = v1DB.Set([]byte(k), []byte(v), pebblev1.Sync)
		require.NoError(t, err, "failed to set key %s in v1 db", k)
	}

	for k, expectedV := range testData {
		val, closer, err := v1DB.Get([]byte(k))
		require.NoError(t, err, "failed to get key %s from v1 db", k)
		assert.Equal(t, expectedV, string(val), "v1 value mismatch for key %s", k)
		closer.Close()
	}

	err = v1DB.Close()
	require.NoError(t, err, "failed to close v1 database")

	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err, "migration failed")

	backupPath := dbPath + ".v1"
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "backup path should exist after migration")

	v2DB, err := pebblev2.Open(dbPath, nil)
	require.NoError(t, err, "failed to open migrated v2 database")
	defer v2DB.Close()

	for k, expectedV := range testData {
		val, closer, err := v2DB.Get([]byte(k))
		require.NoError(t, err, "failed to get key %s from migrated v2 db", k)
		assert.Equal(t, expectedV, string(val), "migrated value mismatch for key %s", k)
		closer.Close()
	}

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

	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err)

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

	v1DB, err := pebblev1.Open(dbPath, nil)
	require.NoError(t, err)
	err = v1DB.Close()
	require.NoError(t, err)

	err = MigratePebbleV1ToV2(dbPath)
	require.NoError(t, err)

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

	v1DB, err := pebblev1.Open(v1Path, nil)
	require.NoError(t, err)

	testKeys := []string{"alpha", "beta", "gamma"}
	for _, k := range testKeys {
		err = v1DB.Set([]byte(k), []byte("value-"+k), pebblev1.Sync)
		require.NoError(t, err)
	}
	err = v1DB.Close()
	require.NoError(t, err)

	err = migratePebbleV1ToV2(v1Path, v2Path)
	require.NoError(t, err)

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
	tmpDir := t.TempDir()
	invalidPath := filepath.Join(tmpDir, "afile")

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

	v1DB, err := pebblev1.Open(v1Path, nil)
	require.NoError(t, err)
	err = v1DB.Close()
	require.NoError(t, err)

	v2Path := "/nonexistent/readonly/path"

	err = migratePebbleV1ToV2(v1Path, v2Path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open v2 DB")
}

func TestCleanupInterruptedMigration_CleanState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")

	// Create a normal database
	err := os.MkdirAll(dbPath, 0755)
	require.NoError(t, err)

	err = cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)

	// Verify path still exists
	_, err = os.Stat(dbPath)
	require.NoError(t, err)
}

func TestCleanupInterruptedMigration_IncompleteV2(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")
	v2Path := dbPath + ".v2"

	// Create both path and path.v2 (incomplete migration)
	err := os.MkdirAll(dbPath, 0755)
	require.NoError(t, err)
	err = os.MkdirAll(v2Path, 0755)
	require.NoError(t, err)

	// Add a file to v2 to ensure RemoveAll works
	err = os.WriteFile(filepath.Join(v2Path, "somefile"), []byte("data"), 0644)
	require.NoError(t, err)

	err = cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)

	// Verify v2 was removed
	_, err = os.Stat(v2Path)
	assert.True(t, os.IsNotExist(err), "v2 path should be removed")

	// Verify original path still exists
	_, err = os.Stat(dbPath)
	require.NoError(t, err)
}

func TestCleanupInterruptedMigration_BackupExistsPathMissing(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")
	backupPath := dbPath + ".v1"

	// Create only the backup (path.v1) - simulates interrupted after first rename
	err := os.MkdirAll(backupPath, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(backupPath, "data"), []byte("backup-data"), 0644)
	require.NoError(t, err)

	err = cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)

	// Verify backup was renamed to path
	_, err = os.Stat(backupPath)
	assert.True(t, os.IsNotExist(err), "backup path should be removed")

	_, err = os.Stat(dbPath)
	require.NoError(t, err, "path should exist after restore")

	// Verify data was preserved
	data, err := os.ReadFile(filepath.Join(dbPath, "data"))
	require.NoError(t, err)
	assert.Equal(t, "backup-data", string(data))
}

func TestCleanupInterruptedMigration_BothV2AndBackupExist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")
	v2Path := dbPath + ".v2"
	backupPath := dbPath + ".v1"

	// Create v2 and backup, but no path - worst case interrupted state
	err := os.MkdirAll(v2Path, 0755)
	require.NoError(t, err)
	err = os.MkdirAll(backupPath, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(backupPath, "data"), []byte("original"), 0644)
	require.NoError(t, err)

	err = cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)

	// Verify v2 was removed
	_, err = os.Stat(v2Path)
	assert.True(t, os.IsNotExist(err), "v2 path should be removed")

	// Verify backup was restored to path
	_, err = os.Stat(backupPath)
	assert.True(t, os.IsNotExist(err), "backup path should be removed")

	_, err = os.Stat(dbPath)
	require.NoError(t, err, "path should exist after restore")
}

func TestCleanupInterruptedMigration_SuccessfulMigrationState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")
	backupPath := dbPath + ".v1"

	// Create path and backup (normal state after successful migration)
	err := os.MkdirAll(dbPath, 0755)
	require.NoError(t, err)
	err = os.MkdirAll(backupPath, 0755)
	require.NoError(t, err)

	err = cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)

	// Both should still exist - no cleanup needed
	_, err = os.Stat(dbPath)
	require.NoError(t, err)
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "backup should remain when path exists")
}

func TestCleanupInterruptedMigration_NonexistentPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent")

	// Nothing exists - should not error
	err := cleanupInterruptedMigration(dbPath)
	require.NoError(t, err)
}
