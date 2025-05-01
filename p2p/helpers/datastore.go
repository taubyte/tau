package helpers

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ipfs/go-datastore"

	pds "github.com/ipfs/go-ds-pebble"

	pebblev1 "github.com/cockroachdb/pebble"
	pebblev2 "github.com/cockroachdb/pebble/v2"
)

func NewDatastore(path string) (datastore.Batching, error) {
	ds, err := pds.NewDatastore(path, nil)
	if err != nil {
		if strings.Contains(err.Error(), "version 1 which is no longer supported") {
			fmt.Println("Migrating Pebble v1 to v2")
			err = MigratePebbleV2ToV1(path)
			if err != nil {
				fmt.Println("Failed to migrate Pebble v1 to v2", err)
				return nil, err
			}
			fmt.Println("Pebble v1 to v2 migration complete")
			return pds.NewDatastore(path, nil)
		}
		return nil, err
	}

	return ds, nil
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

func MigratePebbleV2ToV1(path string) error {
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

	log.Printf("Migration complete. Original DB backed up at: %s", backupPath)
	return nil
}
