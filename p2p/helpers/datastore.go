package helpers

import (
	"fmt"
	"os"
	"strings"

	"github.com/ipfs/go-datastore"

	pds "github.com/ipfs/go-ds-pebble"

	pebblev1 "github.com/cockroachdb/pebble"
	pebblev2 "github.com/cockroachdb/pebble/v2"
)

func NewDatastore(path string) (datastore.Batching, error) {
	// Clean up any interrupted migration before attempting to open
	if err := cleanupInterruptedMigration(path); err != nil {
		return nil, fmt.Errorf("failed to cleanup interrupted migration: %w", err)
	}

	ds, err := pds.NewDatastore(path, nil)
	if err != nil {
		if strings.Contains(err.Error(), "version 1 which is no longer supported") {
			if err = MigratePebbleV1ToV2(path); err != nil {
				return nil, err
			}
			return pds.NewDatastore(path, nil)
		}
		return nil, err
	}

	return ds, nil
}

// cleanupInterruptedMigration handles cases where a previous migration was interrupted
func cleanupInterruptedMigration(path string) error {
	v2Path := path + ".v2"
	backupPath := path + ".v1"

	_, v2Err := os.Stat(v2Path)
	_, backupErr := os.Stat(backupPath)
	_, pathErr := os.Stat(path)

	v2Exists := v2Err == nil
	backupExists := backupErr == nil
	pathExists := pathErr == nil

	// Case 1: path.v2 exists - incomplete migration, remove it to retry fresh
	if v2Exists {
		if err := os.RemoveAll(v2Path); err != nil {
			return fmt.Errorf("failed to remove incomplete migration at %s: %w", v2Path, err)
		}
	}

	// Case 2: path.v1 exists but path doesn't - migration interrupted after backup rename
	// Restore the backup to path so migration can be retried
	if backupExists && !pathExists {
		if err := os.Rename(backupPath, path); err != nil {
			return fmt.Errorf("failed to restore backup from %s to %s: %w", backupPath, path, err)
		}
	}

	return nil
}

func migratePebbleV1ToV2(v1Path, v2Path string) error {
	// Open the existing DB (assumed v1 format)
	oldDB, err := pebblev1.Open(v1Path, nil)
	if err != nil {
		return fmt.Errorf("failed to open v1 DB at %s: %w", v1Path, err)
	}
	defer oldDB.Close()

	// Create the new DB with v2 format
	newDB, err := pebblev2.Open(v2Path, nil)
	if err != nil {
		return fmt.Errorf("failed to open v2 DB at %s: %w", v2Path, err)
	}
	defer newDB.Close()

	iter, err := oldDB.NewIter(nil)
	if err != nil {
		return fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	batch := newDB.NewBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		value := iter.Value()
		if err := batch.Set(key, value, nil); err != nil {
			return fmt.Errorf("failed to set key %q: %w", key, err)
		}
	}

	if err := batch.Commit(pebblev2.Sync); err != nil {
		return fmt.Errorf("failed to commit batch: %w", err)
	}

	return nil
}

func MigratePebbleV1ToV2(path string) error {
	v1Path := path
	v2Path := path + ".v2"
	backupPath := path + ".v1"

	if err := migratePebbleV1ToV2(v1Path, v2Path); err != nil {
		return err
	}

	// Rename original to .v1 and .v2 to final path
	if err := os.Rename(v1Path, backupPath); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", v1Path, backupPath, err)
	}
	if err := os.Rename(v2Path, v1Path); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", v2Path, v1Path, err)
	}

	return nil
}
