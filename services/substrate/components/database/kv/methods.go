package kv

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
)

func (kv *kv) Get(ctx context.Context, key string) (data []byte, err error) {
	data, err = kv.database.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed getting key %s using database %s with error: %v", key, kv.name, err)
	}

	return
}

func (kv *kv) Put(ctx context.Context, key string, v []byte) error {
	// The value and its size go in one batch: one network call and one
	// replicated write instead of two of each. A hoarder-backed database checks
	// the quota on the hoarder from its local data; any other database is
	// checked here by reading every size record.
	var batch kvdb.Batch
	var err error
	if q, ok := kv.database.(hoarderIface.QuotaKVDB); ok {
		batch, err = q.BatchWithMax(ctx, kv.maxSize)
	} else {
		if err = kv.checkValidSize(ctx, v); err != nil {
			return err
		}
		batch, err = kv.database.Batch(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed opening a batch on database %s with error: %v", kv.name, err)
	}
	if err = batch.Put(key, v); err != nil {
		return fmt.Errorf("failed putting %s in database %s with error: %v", key, kv.name, err)
	}
	if err = batch.Put(path.Join("size", key), []byte(strconv.Itoa(len(v)))); err != nil {
		return fmt.Errorf("failed putting size for key %s in database with error: %v", key, err)
	}
	if err = batch.Commit(); err != nil {
		return fmt.Errorf("failed putting %s in database %s with error: %v", key, kv.name, err)
	}

	return nil
}

func (kv *kv) Delete(ctx context.Context, key string) error {
	batch, err := kv.database.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed deleting key %s with error: %v", key, err)
	}
	if err = batch.Delete(key); err != nil {
		return fmt.Errorf("failed deleting key %s with error: %v", key, err)
	}
	if err = batch.Delete(path.Join("size", key)); err != nil {
		return fmt.Errorf("failed deleting key %s with error: %v", key, err)
	}
	if err = batch.Commit(); err != nil {
		return fmt.Errorf("failed deleting key %s with error: %v", key, err)
	}

	return nil
}

func (kv *kv) Close() {
	kv.database.Close()
}

func (kv *kv) List(ctx context.Context, prefix string) ([]string, error) {
	if len(prefix) == 0 {
		entries, err := kv.database.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("listing with empty prefix failed wit: %s", err)
		}

		newList := make([]string, 0)

		for _, entry := range entries {
			if !strings.HasPrefix(entry, "/size") {
				newList = append(newList, entry)
			}
		}

		return newList, nil
	}

	return kv.database.List(ctx, prefix)
}

func (kv *kv) UpdateSize(size uint64) {
	kv.maxSize = size
}

// shortcut: O(n) RPCs over a hoarder-backed database, move to hoarder when Size is hot.
func (kv *kv) Size(ctx context.Context) (uint64, error) {
	used, err := kv.used(ctx)
	if err != nil {
		return 0, err
	}

	if uint64(used) > kv.maxSize {
		return 0, nil
	}

	return kv.maxSize - uint64(used), nil
}
