package database

import (
	"context"
	"encoding/binary"
	"sort"

	"github.com/cespare/xxhash/v2"

	"github.com/taubyte/tau/pkg/database"
)

// digest.go answers what a node holds for one database, as one comparable value.
//
// # Why this exists at all
//
// Placement moves a database by having one node stop holding it and another
// start. "Another node is alive and should hold this" is NOT the same statement
// as "another node HAS this", and the gap between them is where the data goes:
// the holders go dark, membership moves the database elsewhere, the old holders
// come back, read the new placement, and drop the only copies there ever were — because the new holders were alive the whole time and never
// received a row, their source having been down.
//
// So a departing node asks, and compares. Equal digests mean the other node
// holds the same keys with the same values, which for a store whose writes
// converge is a proof rather than a sample.
//
// # Over the four methods, and not over the CRDT
//
// A merkle store already knows whether two replicas agree — its heads say so
// exactly, and more cheaply than this. Reaching for them would put `Heads()` on
// database.Store, which is four methods a map satisfies, and every engine that
// is not a CRDT would then carry a concept it has no answer for.
//
// # Keys AND values
//
// Hashing keys alone would call two nodes converged while one of them held a
// stale value for every key. Lengths are hashed with the bytes so that no two
// different (key, value) pairs can produce the same input — the same
// length-delimiting pkg/placement's Score does, and for the same reason.
func digestOf(ctx context.Context, conn database.KVConn) (uint64, []byte, error) {
	keys, err := conn.List(ctx, "")
	if err != nil {
		return 0, nil, err
	}
	sort.Strings(keys)

	// SORTED, because two nodes must hash the same set in the same order or
	// they never agree. A store's List makes no promise about order.
	sum := xxhash.New()
	var field [8]byte
	write := func(b []byte) {
		binary.BigEndian.PutUint64(field[:], uint64(len(b)))
		sum.Write(field[:])
		sum.Write(b)
	}

	for _, key := range keys {
		value, err := conn.Get(ctx, key)
		if err != nil {
			// A KEY THAT VANISHED BETWEEN THE LIST AND THE READ IS NOT AN
			// ERROR, and it is not skipped either: it is a different set from
			// the one the other node hashed, so the digests differ and the
			// departing node stays. Refusing to go is the safe answer.
			return 0, nil, err
		}
		write([]byte(key))
		write(value)
	}
	return uint64(len(keys)), sum.Sum(nil), nil
}
