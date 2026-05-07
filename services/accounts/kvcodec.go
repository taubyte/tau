package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
)

// CBOR is the on-disk encoding for all structured KV blobs in this service —
// matches patrick / seer / hoarder / tns. Wire (HTTP / P2P) stays JSON.
// Stored types carry `cbor:` tags alongside `json:` tags; fxamacker/cbor/v2
// doesn't fall back to json tags by default, so each round-trip field needs
// an explicit cbor tag.

var ErrNotFound = errors.New("accounts: not found")

func putKV(ctx context.Context, db kvdb.KVDB, key string, v any) error {
	raw, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	if err := db.Put(ctx, key, raw); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func getKV(ctx context.Context, db kvdb.KVDB, key string, v any) error {
	raw, err := db.Get(ctx, key)
	if err != nil {
		if isMissing(err) {
			return ErrNotFound
		}
		return fmt.Errorf("get %s: %w", key, err)
	}
	if len(raw) == 0 {
		return ErrNotFound
	}
	if err := cbor.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return nil
}

// listChildIDs scans keys under prefix and returns the first segment after
// it (the entity id) — used to enumerate Accounts, Plans, etc.
func listChildIDs(ctx context.Context, db kvdb.KVDB, prefix string) ([]string, error) {
	keys, err := db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		if rest == "" {
			continue
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		if _, ok := seen[rest]; ok {
			continue
		}
		seen[rest] = struct{}{}
		out = append(out, rest)
	}
	return out, nil
}

func isMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such") ||
		strings.Contains(msg, "key not found")
}
