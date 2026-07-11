package hoarder

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/kvdb"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// newTestService wires a real kvdb over a standalone mock node — enough to
// exercise the registry, loader, and repair helpers deterministically without a
// dream universe.
func newTestService(t *testing.T) *Service {
	t.Helper()
	node := peer.Mock(t.Context())
	factory := kvdb.New(node)
	db, err := factory.New(logger, "hoarder-test", 5)
	if err != nil {
		t.Fatalf("kvdb create failed: %v", err)
	}
	return &Service{
		node:      node,
		db:        db,
		dbFactory: factory,
		ldr:       newLoader(),
		members:   make(map[string]*member),
		// A fixed 32-byte key so the suite is build-tag agnostic across cipher seams.
		atRestKey: bytes.Repeat([]byte{0x2a}, 32),
	}
}

// addMember injects a live member into the local view (a standalone test node
// has no heartbeat peers). Used to drive placement/reconcile deterministically.
func (srv *Service) addMember(t *testing.T, id string) {
	t.Helper()
	srv.membersLock.Lock()
	srv.members[id] = &member{hb: heartbeat{PeerID: id}, lastSeen: time.Now()}
	srv.membersLock.Unlock()
}

func meta(match string) hoarderIface.MetaData {
	return hoarderIface.MetaData{ProjectId: "proj", ApplicationId: "", Match: match}
}

func TestInstanceHash_Deterministic(t *testing.T) {
	a := instanceHash(meta("users"))
	b := instanceHash(meta("users"))
	c := instanceHash(meta("other"))
	if a != b {
		t.Fatalf("same input gave different hashes: %s vs %s", a, b)
	}
	if a == c {
		t.Fatal("different matchers collided")
	}
}

func TestRegistryMetaRoundTrip(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(meta("users"))

	rec := &RegistryMeta{Kind: hoarderIface.Database, ConfigId: "cfg", ProjectId: "proj", Match: "users"}
	if err := srv.putMeta(ctx, hash, rec); err != nil {
		t.Fatal(err)
	}
	got, err := srv.getMeta(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigId != "cfg" || got.Match != "users" || got.Kind != hoarderIface.Database {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	hashes, err := srv.listMetaHashes(ctx)
	if err != nil || len(hashes) != 1 || hashes[0] != hash {
		t.Fatalf("listMetaHashes = %v, %v", hashes, err)
	}

	if err := srv.deleteMeta(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.getMeta(ctx, hash); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestClaims(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(meta("users"))

	if err := srv.addClaim(ctx, hash, "peerA"); err != nil {
		t.Fatal(err)
	}
	// idempotent re-claim
	if err := srv.addClaim(ctx, hash, "peerA"); err != nil {
		t.Fatal(err)
	}
	if err := srv.addClaim(ctx, hash, "peerB"); err != nil {
		t.Fatal(err)
	}

	claims, err := srv.listClaims(ctx, hash)
	if err != nil || len(claims) != 2 {
		t.Fatalf("listClaims = %v, %v", claims, err)
	}

	if _, ok := srv.claimSince(ctx, hash, "peerA"); !ok {
		t.Fatal("claimSince(peerA) not found")
	}
	if _, ok := srv.claimSince(ctx, hash, "ghost"); ok {
		t.Fatal("claimSince(ghost) should be absent")
	}

	if err := srv.releaseClaim(ctx, hash, "peerA"); err != nil {
		t.Fatal(err)
	}
	claims, _ = srv.listClaims(ctx, hash)
	if len(claims) != 1 || claims[0] != "peerB" {
		t.Fatalf("after release, claims = %v", claims)
	}
}

func TestStashRegistry(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	cid := "bafyTest"

	if err := srv.putStashMeta(ctx, cid, &StashMeta{Target: 3, OwnerHash: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.addStashClaim(ctx, cid, "peerA"); err != nil {
		t.Fatal(err)
	}
	cids, err := srv.listStashCids(ctx)
	if err != nil || len(cids) != 1 || cids[0] != cid {
		t.Fatalf("listStashCids = %v, %v", cids, err)
	}
	claims, err := srv.listStashClaims(ctx, cid)
	if err != nil || len(claims) != 1 {
		t.Fatalf("listStashClaims = %v, %v", claims, err)
	}
	if _, ok := srv.stashClaimSince(ctx, cid, "peerA"); !ok {
		t.Fatal("stashClaimSince not found")
	}
}

func TestLoaderLifecycle(t *testing.T) {
	srv := newTestService(t)
	hash := instanceHash(meta("users"))

	if _, err := srv.load(hash); err != nil {
		t.Fatal(err)
	}
	// load is idempotent (returns the same handle)
	if _, err := srv.load(hash); err != nil {
		t.Fatal(err)
	}

	srv.markClaimed(hash)
	if got := srv.claimedHashes(); len(got) != 1 || got[0] != hash {
		t.Fatalf("claimedHashes = %v", got)
	}

	srv.unmarkClaimed(hash)
	if got := srv.claimedHashes(); len(got) != 0 {
		t.Fatalf("claimedHashes after unmark = %v", got)
	}

	srv.unload(hash)
	// unload of an unloaded hash is a no-op
	srv.unload(hash)
}

func TestIdleUnloadSweep(t *testing.T) {
	srv := newTestService(t)
	hash := instanceHash(meta("users"))
	if _, err := srv.load(hash); err != nil {
		t.Fatal(err)
	}

	// Not idle yet → survives.
	origTTL := hoarderSpecs.IdleTTL
	defer func() { hoarderSpecs.IdleTTL = origTTL }()
	hoarderSpecs.IdleTTL = time.Hour
	srv.idleUnloadSweep()
	srv.ldr.lock.Lock()
	stillLoaded := len(srv.ldr.loaded)
	srv.ldr.lock.Unlock()
	if stillLoaded != 1 {
		t.Fatal("instance unloaded before idle TTL")
	}

	// Past TTL → unloaded.
	hoarderSpecs.IdleTTL = 0
	srv.idleUnloadSweep()
	srv.ldr.lock.Lock()
	after := len(srv.ldr.loaded)
	srv.ldr.lock.Unlock()
	if after != 0 {
		t.Fatal("idle instance was not unloaded")
	}
}

func TestLiveClaimantsAndFleet(t *testing.T) {
	srv := newTestService(t)
	self := srv.node.ID().String()

	// A standalone mock node has an empty topic mesh → only self is live.
	if fs := srv.fleetSize(); fs != 1 {
		t.Fatalf("fleetSize = %d, want 1", fs)
	}
	live := srv.liveClaimants([]string{self, "deadPeer"})
	if len(live) != 1 || live[0] != self {
		t.Fatalf("liveClaimants = %v, want [self]", live)
	}
}

func TestDeleteAndDropLocal(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(meta("users"))

	srv.putMeta(ctx, hash, &RegistryMeta{Kind: hoarderIface.Global, Match: "users"}) //nolint:errcheck
	srv.addClaim(ctx, hash, srv.node.ID().String())                                  //nolint:errcheck
	srv.markClaimed(hash)
	srv.load(hash) //nolint:errcheck

	srv.deleteResource(ctx, hash)
	if _, err := srv.getMeta(ctx, hash); err == nil {
		t.Fatal("meta should be gone after deleteResource")
	}
	if len(srv.claimedHashes()) != 0 {
		t.Fatal("claim should be dropped after deleteResource")
	}

	// dropLocal is the registry-already-gone variant.
	srv.markClaimed(hash)
	srv.dropLocal(ctx, hash)
	if len(srv.claimedHashes()) != 0 {
		t.Fatal("claim should be dropped after dropLocal")
	}
}

func TestReconcile_ClaimsWhenDesired(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(meta("recon"))

	// A single-node fleet: self is the only member, so HRW makes self the sole
	// owner. Reconcile must claim + load it.
	srv.putMeta(ctx, hash, &RegistryMeta{Kind: hoarderIface.Global, Match: "recon"}) //nolint:errcheck
	if srv.isClaimed(hash) {
		t.Fatal("precondition: not claimed yet")
	}
	srv.reconcileOne(ctx, hash)
	if !srv.isClaimed(hash) {
		t.Fatal("reconcile should claim a resource this node owns by HRW")
	}

	// Idempotent: reconciling again keeps exactly one claim.
	srv.reconcileOne(ctx, hash)
	claims, _ := srv.listClaims(ctx, hash)
	if len(claims) != 1 || claims[0] != srv.node.ID().String() {
		t.Fatalf("claims after re-reconcile = %v", claims)
	}
}

func TestReconcile_DropsWhenMetaGone(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(meta("gone"))

	// Held locally but the registry record has vanished (deleted elsewhere) →
	// reconcile drops the local claim.
	srv.addClaim(ctx, hash, srv.node.ID().String()) //nolint:errcheck
	srv.markClaimed(hash)
	srv.load(hash) //nolint:errcheck

	srv.reconcileOne(ctx, hash) // no meta -> dropLocal
	if srv.isClaimed(hash) {
		t.Fatal("reconcile should drop a claim whose meta is gone")
	}
}

func TestReconcile_ReleasesWhenNotDesiredAndCovered(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()
	hash := instanceHash(meta("shed"))

	// Big fleet where self is not among the HRW owners, and enough OTHER live
	// members already hold it → self conservatively sheds.
	srv.putMeta(ctx, hash, &RegistryMeta{Kind: hoarderIface.Global, Match: "shed"}) //nolint:errcheck
	for _, m := range []string{"peerA", "peerB", "peerC", "peerD", "peerE"} {
		srv.addMember(t, m)
	}
	members := srv.activeMembers()
	desired := placementDesired(hash, members, targetReplicas(len(members)))
	if contains(desired, self) {
		t.Skip("self happens to be a desired owner for this run; release path not exercised")
	}
	// self holds it, and the desired owners also hold it (they're live members).
	srv.addClaim(ctx, hash, self) //nolint:errcheck
	srv.markClaimed(hash)
	srv.load(hash) //nolint:errcheck
	for _, d := range desired {
		srv.addClaim(ctx, hash, d) //nolint:errcheck
	}

	srv.reconcileOne(ctx, hash)
	if srv.isClaimed(hash) {
		t.Fatal("self should shed a claim it no longer owns once coverage is met")
	}
}

func TestPackage(t *testing.T) {
	if Package() == nil {
		t.Fatal("Package() returned nil")
	}
}

func kvBody(op string) command.Body {
	return command.Body{
		hoarderSpecs.BodyKind:    int(hoarderIface.Global),
		hoarderSpecs.BodyProject: "kvp",
		hoarderSpecs.BodyMatch:   "/kv",
		hoarderSpecs.BodyKVOp:    op,
	}
}

func TestAuctionFromBody(t *testing.T) {
	a, err := auctionFromBody(kvBody(hoarderSpecs.KVGet))
	if err != nil {
		t.Fatal(err)
	}
	if a.MetaType != hoarderIface.Global || a.Meta.ProjectId != "kvp" || a.Meta.Match != "/kv" {
		t.Fatalf("auctionFromBody = %+v", a)
	}
	if _, err := auctionFromBody(command.Body{}); err == nil {
		t.Fatal("expected error for a body missing kind")
	}
}

func TestResolveInstance_OwnerServes(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	body := kvBody(hoarderSpecs.KVGet)
	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})

	// Single-node fleet: HRW makes self the sole owner → records meta, claims,
	// loads, and serves (no redirect).
	handle, gotHash, redirect, err := srv.resolveInstance(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if redirect != nil || handle == nil || gotHash != hash {
		t.Fatalf("owner resolve = handle:%v redirect:%v hash:%s", handle != nil, redirect, gotHash)
	}
	if !srv.isClaimed(hash) {
		t.Fatal("owner resolve should claim the instance")
	}

	// Second call: already claimant → serves directly, single claim.
	handle, _, redirect, err = srv.resolveInstance(ctx, body)
	if err != nil || redirect != nil || handle == nil {
		t.Fatalf("claimant resolve failed: %v / %v", err, redirect)
	}
	if claims, _ := srv.listClaims(ctx, hash); len(claims) != 1 {
		t.Fatalf("expected exactly one claim, got %v", claims)
	}
}

func TestResolveInstance_RedirectsToOwners(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()
	for _, m := range []string{"peerA", "peerB", "peerC", "peerD", "peerE", "peerF"} {
		srv.addMember(t, m)
	}
	members := srv.activeMembers()
	target := targetReplicas(len(members))

	// Find a resource this node does NOT own so we exercise the redirect path
	// deterministically (self's id is fixed for the run).
	var match, hash string
	var desired []string
	for i := 0; ; i++ {
		match = fmt.Sprintf("/kv/%d", i)
		hash = instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: match})
		desired = placementDesired(hash, members, target)
		if !contains(desired, self) {
			break
		}
		if i > 2000 {
			t.Fatal("could not find a resource not owned by self")
		}
	}

	body := command.Body{
		hoarderSpecs.BodyKind:    int(hoarderIface.Global),
		hoarderSpecs.BodyProject: "kvp",
		hoarderSpecs.BodyMatch:   match,
		hoarderSpecs.BodyKVOp:    hoarderSpecs.KVGet,
	}
	handle, _, redirect, err := srv.resolveInstance(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if handle != nil || redirect == nil {
		t.Fatal("non-owner must redirect, not serve")
	}
	if redirect[hoarderSpecs.BodyCode] != hoarderSpecs.CodeNotReplica {
		t.Fatalf("redirect code = %v", redirect[hoarderSpecs.BodyCode])
	}
	if peers, _ := redirect[hoarderSpecs.BodyPeers].([]string); !reflect.DeepEqual(peers, desired) {
		t.Fatalf("redirect peers %v != HRW desired %v", peers, desired)
	}
}

func TestKVOps(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})
	handle, err := srv.load(hash)
	if err != nil {
		t.Fatal(err)
	}

	put := kvBody(hoarderSpecs.KVPut)
	put[hoarderSpecs.BodyKey] = "k1"
	put[hoarderSpecs.BodyValue] = []byte("v1")
	if _, err := srv.kvPut(ctx, handle, hash, put); err != nil {
		t.Fatal(err)
	}

	get := kvBody(hoarderSpecs.KVGet)
	get[hoarderSpecs.BodyKey] = "k1"
	resp, err := srv.kvGet(ctx, handle, get)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp[hoarderSpecs.BodyValue].([]byte)) != "v1" {
		t.Fatalf("get = %v", resp)
	}

	// Missing key → not-found code.
	get[hoarderSpecs.BodyKey] = "ghost"
	resp, _ = srv.kvGet(ctx, handle, get)
	if resp[hoarderSpecs.BodyCode] != hoarderSpecs.CodeNotFound {
		t.Fatalf("expected not-found, got %v", resp)
	}

	list := kvBody(hoarderSpecs.KVList)
	resp, err = srv.kvList(ctx, handle, list)
	if err != nil || len(resp[hoarderSpecs.BodyKeys].([]string)) != 1 {
		t.Fatalf("list = %v / %v", resp, err)
	}

	// Batch put + delete.
	batchBody := kvBody(hoarderSpecs.KVBatch)
	batchBody[hoarderSpecs.BodyOps] = []interface{}{
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: "k2", hoarderSpecs.BodyValue: []byte("v2")},
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVDelete, hoarderSpecs.BodyKey: "k1"},
	}
	if _, err := srv.kvBatch(ctx, handle, hash, batchBody); err != nil {
		t.Fatal(err)
	}
	// Read through kvGet (which decrypts under -tags ee; identity under OSS).
	getK2 := kvBody(hoarderSpecs.KVGet)
	getK2[hoarderSpecs.BodyKey] = "k2"
	if resp, _ := srv.kvGet(ctx, handle, getK2); string(resp[hoarderSpecs.BodyValue].([]byte)) != "v2" {
		t.Fatal("batch put not applied")
	}
	if _, err := handle.Get(ctx, "k1"); err == nil {
		t.Fatal("batch delete not applied")
	}

	del := kvBody(hoarderSpecs.KVDelete)
	del[hoarderSpecs.BodyKey] = "k2"
	if _, err := srv.kvDelete(ctx, handle, hash, del); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.kvSync(ctx, handle, kvBody(hoarderSpecs.KVSync)); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.kvListRegex(ctx, handle, kvBody(hoarderSpecs.KVListRegex)); err != nil {
		t.Fatal(err)
	}
}

func TestReplicateWrite_NoCoClaimant(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})
	srv.addClaim(ctx, hash, srv.node.ID().String()) //nolint:errcheck

	// No co-claimant → local-only ack (must not panic without a kvStream).
	srv.replicateWrite(ctx, hash, kvBody(hoarderSpecs.KVPut))

	// A no-barrier push is skipped entirely.
	b := kvBody(hoarderSpecs.KVPut)
	b[hoarderSpecs.BodyNoBarrier] = true
	srv.replicateWrite(ctx, hash, b)
}

// TestReplicateWrite_UnreachableCoOwnerBounded pins the K=2 barrier's boundedness
// under an asymmetric partition: a co-owner that stays live-per-membership yet is
// undialable must not spin the handler goroutine forever. The handler ctx is the
// service lifetime (never per-request), so only the 3× LivenessTimeout bound stops
// the retry loop — this test fails (times out) if that bound regresses.
func TestReplicateWrite_UnreachableCoOwnerBounded(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	// A real kv replication client over this node so the barrier push actually
	// dials — and fails, because the co-owner has no link in the mocknet.
	kvStream, err := streamClient.New(srv.node, protocolCommon.HoarderProtocol)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kvStream.Close)
	srv.kvStream = kvStream

	// Shrink the liveness window so 3× of it is sub-second; restore on cleanup
	// (mirrors the fastConvergence pattern in tests/replication_test.go).
	origLive := hoarderSpecs.LivenessTimeout
	origRetry := hoarderSpecs.BarrierRetryInterval
	hoarderSpecs.LivenessTimeout = 200 * time.Millisecond
	hoarderSpecs.BarrierRetryInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		hoarderSpecs.LivenessTimeout = origLive
		hoarderSpecs.BarrierRetryInterval = origRetry
	})

	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})
	srv.addClaim(ctx, hash, srv.node.ID().String()) //nolint:errcheck

	// A live co-owner with a VALID but unreachable peer ID: peerCore.Decode
	// succeeds, so it is a real placement target, but no mocknet link exists so
	// every dial fails. This is the "live per gossip, undialable from here" state.
	const unreachable = "12D3KooWQFwFDkkGnQ8y23wTUZ1kV3RVpZWkTgy5rd3jyvvV2ypM"
	if _, err := peerCore.Decode(unreachable); err != nil {
		t.Fatalf("test peer id must decode: %v", err)
	}
	srv.addMember(t, unreachable)
	srv.addClaim(ctx, hash, unreachable) //nolint:errcheck

	// Simulate the ongoing gossip heartbeats that keep the co-owner "live": refresh
	// its lastSeen faster than the liveness window so activeMembers never drops it,
	// isolating the deadline as the only thing that can end the loop.
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(hoarderSpecs.LivenessTimeout / 4)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				srv.membersLock.Lock()
				if m := srv.members[unreachable]; m != nil {
					m.lastSeen = time.Now()
				}
				srv.membersLock.Unlock()
			}
		}
	}()
	t.Cleanup(func() { close(stop) })

	bound := 3 * hoarderSpecs.LivenessTimeout
	done := make(chan struct{})
	start := time.Now()
	go func() {
		srv.replicateWrite(ctx, hash, kvBody(hoarderSpecs.KVPut))
		close(done)
	}()

	select {
	case <-done:
		// Must have honored the barrier window (not early-exited via some other
		// path) yet still returned — proving the loop is bounded, not spinning.
		if elapsed := time.Since(start); elapsed < bound {
			t.Fatalf("replicateWrite returned in %s, before the %s barrier window", elapsed, bound)
		}
	case <-time.After(bound + 5*time.Second):
		t.Fatal("replicateWrite did not return within the barrier window — the bound regressed (spinning on an undialable live co-owner)")
	}
}

func TestStatusLoadUnload(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	body := command.Body{hoarderSpecs.BodyProject: "kvp", hoarderSpecs.BodyMatch: "/kv"}
	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})
	srv.addClaim(ctx, hash, srv.node.ID().String()) //nolint:errcheck

	if _, err := srv.loadHandler(ctx, body); err != nil {
		t.Fatal(err)
	}
	resp, err := srv.statusHandler(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if resp["loaded"] != true {
		t.Fatalf("status loaded = %v", resp["loaded"])
	}
	if _, err := srv.unloadHandler(body); err != nil {
		t.Fatal(err)
	}
	if srv.isLoaded(hash) {
		t.Fatal("instance should be unloaded")
	}
}

func TestStashReceive_Success(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	data := []byte("hello stash payload")
	gotCid, err := srv.node.AddFileForCid(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	cidStr := gotCid.String()

	var buf bytes.Buffer
	writeStashHeader(t, &buf, cidStr, 1, false)
	buf.Write(data)

	srv.stashReceive(ctx, &buf)

	resp, err := response.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp[hoarderSpecs.BodyCid] != cidStr {
		t.Fatalf("ack = %v, want cid %s", resp, cidStr)
	}
	if _, ok := srv.stashClaimSince(ctx, cidStr, srv.node.ID().String()); !ok {
		t.Fatal("stash claim should be recorded on success")
	}
}

func TestStashReceive_CidMismatch(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	var buf bytes.Buffer
	// Declare a CID that the bytes will not hash to → must be rejected.
	writeStashHeader(t, &buf, "bafyDeclaredButWrong", 1, false)
	buf.Write([]byte("actual payload hashes to something else"))

	srv.stashReceive(ctx, &buf)

	resp, err := response.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["error"].(string); !ok {
		t.Fatalf("expected an error ack on cid mismatch, got %v", resp)
	}
	// Nothing should have been claimed.
	cids, _ := srv.listStashCids(ctx)
	if len(cids) != 0 {
		t.Fatalf("mismatched push must not create a stash record, got %v", cids)
	}
}

func writeStashHeader(t *testing.T, w *bytes.Buffer, cid string, target int, fanout bool) {
	t.Helper()
	h := command.New(hoarderSpecs.StashHeader, command.Body{
		hoarderSpecs.BodyCid:    cid,
		hoarderSpecs.BodyTarget: target,
		hoarderSpecs.BodyFanout: fanout,
	})
	if err := h.Encode(w); err != nil {
		t.Fatal(err)
	}
}

func TestStashErr(t *testing.T) {
	var buf bytes.Buffer
	stashErr(&buf, "boom")
	resp, err := response.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "boom" {
		t.Fatalf("stashErr response = %v, want error=boom", resp)
	}
}

func TestMetaAuction(t *testing.T) {
	rec := &RegistryMeta{Kind: hoarderIface.Storage, ConfigId: "c", ProjectId: "p", ApplicationId: "a", Match: "m", Branch: "b"}
	a := metaAuction(rec)
	if a.MetaType != hoarderIface.Storage || a.Meta.ConfigId != "c" || a.Meta.Match != "m" || a.Meta.Branch != "b" {
		t.Fatalf("metaAuction = %+v", a)
	}
}

func TestCheckMatch(t *testing.T) {
	if err := checkMatch(false, "users", "users", "n"); err != nil {
		t.Fatalf("exact match should pass: %v", err)
	}
	if err := checkMatch(false, "users", "other", "n"); err == nil {
		t.Fatal("exact mismatch should fail")
	}
	if err := checkMatch(true, "user42", "^user[0-9]+$", "n"); err != nil {
		t.Fatalf("regex match should pass: %v", err)
	}
	if err := checkMatch(true, "nope", "^user[0-9]+$", "n"); err == nil {
		t.Fatal("regex mismatch should fail")
	}
	if err := checkMatch(true, "x", "[", "n"); err == nil {
		t.Fatal("invalid regex should error")
	}
}

func TestClaimAndLoad_Global(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	a := &hoarderIface.Auction{MetaType: hoarderIface.Global, Meta: meta("gload")}
	hash := instanceHash(a.Meta)

	// Global skips TNS validation, so claimAndLoad runs fully on a mock node.
	if err := srv.claimAndLoad(ctx, hash, a); err != nil {
		t.Fatal(err)
	}
	if claimed := srv.claimedHashes(); len(claimed) != 1 || claimed[0] != hash {
		t.Fatalf("claimAndLoad should mark claimed, got %v", claimed)
	}
	if _, err := srv.getMeta(ctx, hash); err != nil {
		t.Fatal("claimAndLoad should write meta")
	}
	if _, ok := srv.claimSince(ctx, hash, srv.node.ID().String()); !ok {
		t.Fatal("claimAndLoad should write this node's claim")
	}

	// configDeleted is false for Global (no backing TNS config to lose).
	if srv.configDeleted(&RegistryMeta{Kind: hoarderIface.Global}) {
		t.Fatal("Global resources are never config-deleted")
	}
}

func TestValidateConfig_Global(t *testing.T) {
	srv := newTestService(t)
	if err := srv.validateConfig(&hoarderIface.Auction{MetaType: hoarderIface.Global}); err != nil {
		t.Fatalf("Global validation should pass: %v", err)
	}
}

func TestRecoverClaims(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()
	hash := instanceHash(meta("recover"))

	// Persist a claim as if a previous incarnation held it.
	srv.putMeta(ctx, hash, &RegistryMeta{Kind: hoarderIface.Global, Match: "recover"}) //nolint:errcheck
	srv.addClaim(ctx, hash, self)                                                      //nolint:errcheck

	srv.recoverClaims(ctx)
	claimed := srv.claimedHashes()
	if len(claimed) != 1 || claimed[0] != hash {
		t.Fatalf("recoverClaims should re-mark the claim, got %v", claimed)
	}
}
