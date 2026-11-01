package database

import (
	"context"
	"encoding/binary"
	"sort"

	"github.com/cespare/xxhash/v2"

	"github.com/taubyte/tau/pkg/database"
)

// digestOf is what a node holds for one database, as one comparable value. A
// departing node compares digests before dropping, because "another node should hold
// this" is not "another node has this". Computed over the store's four methods
// rather than CRDT heads, so non-CRDT engines need nothing extra. Keys and values are
// hashed, each length-delimited so different pairs cannot collide.
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
