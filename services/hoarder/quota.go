package hoarder

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/taubyte/tau/core/kvdb"
)

// dbUsage is the sum of the size records (size/<key>, written by substrate next
// to each value) in an instance, as this node's local data has it. It lives in
// memory only, never in the CRDT, and is guarded by the instance write lock.
// It drifts for writes merged from other replicas, so a refusal always
// rescans first.
type dbUsage struct {
	known bool
	bytes int64
}

// scan recounts usage from local data: one local list and local reads.
func (u *dbUsage) scan(ctx context.Context, handle kvdb.KVDB) error {
	keys, err := handle.List(ctx, "size/")
	if err != nil {
		return fmt.Errorf("listing sizes failed with: %w", err)
	}
	var total int64
	for _, k := range keys {
		v, err := handle.Get(ctx, k)
		if err != nil {
			return fmt.Errorf("reading size %s failed with: %w", k, err)
		}
		total += parseSize(v)
	}
	u.bytes, u.known = total, true
	return nil
}

// parseSize reads a size record. A user key under size/ can hold anything:
// a value that is not a non-negative number counts as 0.
func parseSize(v []byte) int64 {
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// forgetUsageIfSize drops the in-memory usage when a single-key write touches
// a size record, so the next quota check rescans. Call under the write lock.
func (srv *Service) forgetUsageIfSize(hash, key string) {
	if strings.HasPrefix(path.Clean("/"+key), "/size/") {
		srv.usageOf(hash).known = false
	}
}
