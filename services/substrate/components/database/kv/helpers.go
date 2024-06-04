package kv

import (
	"context"
	"fmt"
	"strconv"
)

func (kv *kv) used(ctx context.Context) (int, error) {
	var used int
	sizes, err := kv.database.List(ctx, "size/")
	if err != nil {
		return 0, fmt.Errorf("failed getting sizes with error: %v", err)
	}

	for _, key := range sizes {
		sizeByte, err := kv.database.Get(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("getting Size for key %s failed with %w", key, err)
		}

		size, err := strconv.Atoi(string(sizeByte))
		if err != nil {
			return 0, fmt.Errorf("failed converting byte to int %s failed with %w", key, err)
		}

		used += int(size)
	}
	return used, nil
}

func (kv *kv) checkValidSize(ctx context.Context, input []byte) error {
	used, err := kv.used(ctx)
	if err != nil {
		return fmt.Errorf("getting usage for in kvdb failed with %w", err)
	}

	inputSize := len(input)

	if kv.maxSize < uint64(used+inputSize) {
		return fmt.Errorf("no space left for input")
	}

	return nil
}
