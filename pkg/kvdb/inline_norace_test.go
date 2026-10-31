//go:build !race

// The race detector makes sync.Pool drop what is put in it at random, and
// verifying what a snapshot carries works in storage taken from one, so
// allocations are counted only without it.

package kvdb

import (
	"crypto/sha256"
	"testing"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

// TestVerifyingACarriedStateAllocatesWhatItsBlocksDo: checking what a
// snapshot carries allocates what decoding its blocks does, as a fetched
// block is decoded, and what rebuilding its StateRoot does, and next to
// nothing besides: the links it waits for are digests in pooled storage.
func TestVerifyingACarriedStateAllocatesWhatItsBlocksDo(t *testing.T) {
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedKeys(t, a, 1000, "k")
	compact(t, a)
	put(t, a, "/k0007", "again")
	compact(t, a)
	_, root := headSnapshot(t, a)
	stc := cid.MustParse(root.GetState())
	sr, err := carriedRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	cids := make([]cid.Cid, len(root.GetInline()))
	for i, raw := range root.GetInline() {
		cids[i] = cidOf(sha256.Sum256(raw))
	}
	blocksAlone := testing.AllocsPerRun(50, func() {
		nd, err := sr.node()
		if err != nil {
			t.Fatal(err)
		}
		if nd.Cid() != stc {
			t.Fatal("the rebuilt StateRoot is not the state")
		}
		for i, raw := range root.GetInline() {
			nd, err := statetree.ParseBlock(cids[i], raw)
			if err != nil {
				t.Fatal(err)
			}
			var buf [32]cid.Cid
			if _, err := blockLinks(nd, buf[:0]); err != nil {
				t.Fatal(err)
			}
		}
	})
	verify := testing.AllocsPerRun(50, func() {
		cs := takeCarried()
		if err := cs.verify(stc, root); err != nil {
			t.Fatal(err)
		}
		cs.putBack()
	})
	t.Logf("%d blocks inline: verify %.0f allocations, decoding them and rebuilding the root %.0f", len(root.GetInline()), verify, blocksAlone)
	// Each tree's cid is read into a string of its own.
	if verify > blocksAlone+2*float64(len(root.GetTrees()))+2 {
		t.Fatalf("verifying %d blocks allocated %.0f times, decoding them %.0f", len(root.GetInline()), verify, blocksAlone)
	}
}
