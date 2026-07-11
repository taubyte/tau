package hoarder

import (
	"bytes"
	"testing"
	"time"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// fakeAssetTns serves a canned assets index: Lookup lists the keys, Fetch
// returns each key's CID value. Everything else panics via the embedded nil
// interface — exactly what a unit test wants.
type fakeAssetTns struct {
	ifaceTns.Client
	keys map[string]string // "/assets/<hash>" → cid
}

func (f *fakeAssetTns) Lookup(ifaceTns.Query) (interface{}, error) {
	out := make([]string, 0, len(f.keys))
	for k := range f.keys {
		out = append(out, k)
	}
	return out, nil
}

func (f *fakeAssetTns) Fetch(path ifaceTns.Path) (ifaceTns.Object, error) {
	return fakeObject{v: f.keys["/"+path.String()]}, nil
}

type fakeObject struct {
	ifaceTns.Object
	v string
}

func (f fakeObject) Interface() interface{} { return f.v }

func TestAssetSweep_AdoptsUnclaimedAndSkipsReplicated(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()

	// Seed bytes on the node — the shape of an asset whose CID is recorded in
	// TNS but was never stash-claimed.
	cid, err := srv.node.AddFile(bytes.NewReader([]byte("built artifact bytes")))
	if err != nil {
		t.Fatalf("seeding file failed: %v", err)
	}
	srv.tnsClient = &fakeAssetTns{keys: map[string]string{"/assets/somehash": cid}}

	cids, err := srv.tnsAssetCids()
	if err != nil {
		t.Fatalf("listing asset cids failed: %v", err)
	}
	if len(cids) != 1 || cids[0] != cid {
		t.Fatalf("unexpected cid list: %v", cids)
	}

	adopted, skipped := srv.adoptAssets(ctx, cids)
	if adopted != 1 || skipped != 0 {
		t.Fatalf("expected 1 adopted / 0 skipped, got %d / %d", adopted, skipped)
	}
	if _, ok := srv.stashClaimSince(ctx, cid, self); !ok {
		t.Fatal("adopted asset has no stash claim")
	}
	meta, err := srv.getStashMeta(ctx, cid)
	if err != nil || meta.Target != 1 { // single-node fleet clamps the target to 1
		t.Fatalf("unexpected stash meta: %+v, err %v", meta, err)
	}

	// Second pass: already at target → skipped, nothing re-fetched.
	adopted, skipped = srv.adoptAssets(ctx, cids)
	if adopted != 0 || skipped != 1 {
		t.Fatalf("expected 0 adopted / 1 skipped, got %d / %d", adopted, skipped)
	}
}

func TestAssetSweep_UnfetchableCidReportedNotClaimed(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	// A CID nothing holds: the fetch must time out and leave no claim behind.
	restore := hoarderSpecs.AssetSweepFetchTimeout
	hoarderSpecs.AssetSweepFetchTimeout = 300 * time.Millisecond
	defer func() { hoarderSpecs.AssetSweepFetchTimeout = restore }()

	missing := "QmYwAPJzv5CZsnAzt8auVZRn1pfejgxk2GYDdVQGWvVFrH"
	adopted, skipped := srv.adoptAssets(ctx, []string{missing})
	if adopted != 0 || skipped != 0 {
		t.Fatalf("expected nothing adopted/skipped, got %d / %d", adopted, skipped)
	}
	if _, ok := srv.stashClaimSince(ctx, missing, srv.node.ID().String()); ok {
		t.Fatal("unfetchable cid must not be claimed")
	}
}

func TestMetasAndStashStatusHandlers(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()

	hash := instanceHash(meta("users"))
	if err := srv.putMeta(ctx, hash, &RegistryMeta{
		Kind: hoarderIface.Database, ConfigId: "cfg-1", ProjectId: "proj", Match: "users", Branch: "main",
	}); err != nil {
		t.Fatalf("putMeta failed: %v", err)
	}

	resp, err := srv.metasHandler(ctx, command.Body{hoarderSpecs.BodyHashes: []string{hash, "absent-hash"}})
	if err != nil {
		t.Fatalf("metasHandler failed: %v", err)
	}
	got := maps.SafeInterfaceToStringKeys(resp[hoarderSpecs.BodyMetas])
	if len(got) != 1 {
		t.Fatalf("expected 1 meta, got %d", len(got))
	}
	entry := maps.SafeInterfaceToStringKeys(got[hash])
	if maps.TryString(entry, hoarderSpecs.BodyMatch) != "users" ||
		maps.TryString(entry, hoarderSpecs.BodyProject) != "proj" ||
		maps.TryString(entry, hoarderSpecs.BodyConfig) != "cfg-1" {
		t.Fatalf("meta fields wrong: %v", entry)
	}

	cid := "QmSomeCid"
	if err := srv.addStashClaim(ctx, cid, self); err != nil {
		t.Fatalf("addStashClaim failed: %v", err)
	}
	resp, err = srv.stashStatusHandler(ctx, command.Body{hoarderSpecs.BodyCids: []string{cid, "unknown-cid"}})
	if err != nil {
		t.Fatalf("stashStatusHandler failed: %v", err)
	}
	claims := maps.SafeInterfaceToStringKeys(resp[hoarderSpecs.BodyClaims])
	if n, _ := maps.Int(claims, cid); n != 1 {
		t.Fatalf("expected 1 live claim for %s, got %v", cid, claims[cid])
	}
	if n, _ := maps.Int(claims, "unknown-cid"); n != 0 {
		t.Fatalf("expected 0 claims for unknown cid, got %v", claims["unknown-cid"])
	}
}
