//go:build !race

package kvdb

import (
	"context"
	"testing"
	"time"

	query "github.com/ipfs/go-datastore/query"
	dstest "github.com/ipfs/go-datastore/test"
)

func TestDatastoreSuite(t *testing.T) {
	ctx := context.Background()

	numReplicasOld := numReplicas
	numReplicas = 1
	defer func() {
		numReplicas = numReplicasOld
	}()
	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 200 * 1024 * 1024 // 200 MB
	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()
	dstest.SubtestAll(t, replicas[0])
	time.Sleep(time.Second)

	for _, r := range replicas {
		q := query.Query{KeysOnly: true}
		results, err := r.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		// nolint:errcheck
		defer results.Close()
		rest, err := results.Rest()
		if err != nil {
			t.Fatal(err)
		}
		if len(rest) != 0 {
			t.Error("all elements in the suite should be gone")
		}
	}
}
