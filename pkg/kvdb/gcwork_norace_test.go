//go:build !race

// sync.Pool drops entries at random under the race detector, so allocations
// are counted without it.

package kvdb

import (
	"context"
	"slices"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

func TestARetirementAllocatesLittleBeyondItsDiffs(t *testing.T) {
	ctx := context.Background()
	for _, changes := range []int{1, 32, 256} {
		r, _ := warmReplica(t, 4000)
		old := stateOf(t, r)
		pin, err := r.pinState(ctx, old, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		for i := range changes {
			if err := r.Put(ctx, warmKey((i*7919)%4000), warmValue(i, 1)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		r.gcMu.Lock()
		gone := 0
		plan := func() {
			ch := r.changeRoots()
			ch.remove(old)
			if err := ch.prepare(ctx, newTxn(r.store)); err != nil {
				t.Fatal(err)
			}
			gone = len(ch.plan.gone)
		}
		retired := r.gc.roots[keyOf(old)].trees()
		var live [3][]cid.Cid
		for _, lr := range r.gc.roots {
			for kind, c := range lr.trees() {
				if lr.c != old && c.Defined() && !slices.Contains(live[kind], c) {
					live[kind] = append(live[kind], c)
				}
			}
		}
		var pairs [][2]cid.Cid
		for kind, c := range retired {
			if c.Defined() && !slices.Contains(live[kind], c) {
				for _, l := range live[kind] {
					pairs = append(pairs, [2]cid.Cid{c, l})
				}
			}
		}
		var d statetree.Differ
		diffs := func() {
			for _, p := range pairs {
				if _, _, err := d.Nodes(ctx, stateTree(r.dagService, p[0]), stateTree(r.dagService, p[1])); err != nil {
					t.Fatal(err)
				}
			}
		}
		planAllocs := testing.AllocsPerRun(10, plan)
		diffAllocs := testing.AllocsPerRun(10, diffs)
		r.gcMu.Unlock()
		t.Logf("%d changes: %d blocks unreached; the plan allocates %.0f times, its %d diffs %.0f", changes, gone, planAllocs, len(pairs), diffAllocs)
		if planAllocs > diffAllocs+48 {
			t.Errorf("%d changes: the plan allocates %.0f times, %.0f more than its diffs", changes, planAllocs, planAllocs-diffAllocs)
		}
		if err := pin.Release(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
