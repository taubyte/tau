package kv

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
)

func (kv *kv) Get(ctx context.Context, key string) (data []byte, err error) {
	data, err = kv.database.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed getting key %s using database %s with error: %v", key, kv.name, err)
	}

	return
}

func (kv *kv) Put(ctx context.Context, key string, v []byte) error {
	err := kv.checkValidSize(ctx, v)
	if err != nil {
		return err
	}

	// Register actual data into the database
	err = kv.database.Put(ctx, key, v)
	if err != nil {
		return fmt.Errorf("failed putting %s in database %s with error: %v", key, kv.name, err)
	}

	// Register size of input
	sizeString := strconv.Itoa(len(v))
	err = kv.database.Put(ctx, path.Join("size", key), []byte(sizeString))
	if err != nil {
		return fmt.Errorf("failed putting size for key %s in database with error: %v", key, err)
	}

	return nil
}

func (kv *kv) Delete(ctx context.Context, key string) error {
	// Delete key
	err := kv.database.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("failed deleting key %s with error: %v", key, err)
	}

	// Delete size of key
	err = kv.database.Delete(ctx, path.Join("size", key))
	if err != nil {
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

		var newList []string

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
