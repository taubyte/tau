package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"

	keypair "github.com/taubyte/tau/p2p/keypair"

	hoarder_client "github.com/taubyte/tau/clients/p2p/hoarder"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	con "github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	streams "github.com/taubyte/tau/p2p/streams/service"
	"github.com/taubyte/tau/pkg/config"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	multihash "github.com/taubyte/tau/utils/multihash"

	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/common"
	service "github.com/taubyte/tau/services/hoarder"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
)

func TestHoarderClient(t *testing.T) {
	ctx := context.Background()

	srvRoot := t.TempDir()

	cfg, err := config.New(
		config.WithRoot(srvRoot),
		config.WithP2PListen([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)}),
		config.WithP2PAnnounce([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)}),
		config.WithSwarmKey(common.SwarmKey()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srv, err := service.New(ctx, cfg)
	if err != nil {
		t.Errorf("Error creating Service with: %s", err)
		return
	}
	defer srv.Close()

	peerC, err := peer.New( // consumer
		ctx,
		nil,
		keypair.NewRaw(),
		common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11012)},
		nil,
		true,
		false,
	)

	if err != nil {
		t.Errorf("Creating new peer error `%s`", err.Error())
		return
	}

	// give service some time to start
	time.Sleep(1 * time.Second)

	err = peerC.Peer().Connect(ctx, peercore.AddrInfo{ID: srv.Node().ID(), Addrs: srv.Node().Peer().Addrs()})
	if err != nil {
		t.Errorf("Connect to peer returned `%s`", err.Error())
		return
	}

	// give time for peers to discover each other
	time.Sleep(1 * time.Second)

	client, err := hoarder_client.New(ctx, peerC)
	if err != nil {
		t.Error(err)
		return
	}

	// Peers() scopes the client to the hoarder we connected to.
	hc := client.Peers(srv.Node().ID())

	// Push a blob and confirm the receiver claims it (rare, since a lone
	// hoarder is below the default stash replica target).
	data := []byte("hoarder client test blob")
	cid, err := srv.Node().AddFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := hc.Stash(cid, bytes.NewReader(data)); err != nil {
		t.Fatalf("Stash: %v", err)
	}

	list, err := hc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, c := range list {
		if c == cid {
			found = true
		}
	}
	if !found {
		t.Fatalf("List %v does not contain stashed cid %s", list, cid)
	}

	// A lone hoarder clamps the replica target to 1, so a single claim already
	// satisfies it and nothing reads back as rare.
	rare, err := hc.Rare()
	if err != nil {
		t.Fatalf("Rare: %v", err)
	}
	if len(rare) != 0 {
		t.Fatalf("expected nothing rare on a single hoarder, got %v", rare)
	}

	// ReplicasOf a resource with no placement resolves to an empty set.
	peers, err := hc.ReplicasOf(hoarderIface.Global, "proj", "", "unplaced")
	if err != nil {
		t.Fatalf("ReplicasOf: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected no replicas for an unplaced resource, got %v", peers)
	}

	// Place a Global resource with a first-touch KVDB write: HRW self-election
	// records the meta and the lone hoarder (sole owner) claims it synchronously.
	// Then confirm ReplicasOf resolves the claiming hoarder.
	place, err := hc.KVDB(hoarderIface.Global, "proj", "", "placed", "main")
	if err != nil {
		t.Fatalf("KVDB(placed): %v", err)
	}
	if err := place.Put(ctx, "touch", []byte("x")); err != nil {
		t.Fatalf("first-touch Put: %v", err)
	}
	place.Close()
	placed := false
	for range 30 {
		peers, err := hc.ReplicasOf(hoarderIface.Global, "proj", "", "placed")
		if err == nil && len(peers) == 1 && peers[0] == srv.Node().ID() {
			placed = true
			break
		}
		time.Sleep(time.Second)
	}
	if !placed {
		t.Fatal("expected the hoarder to claim and resolve as the replica")
	}

	// A push whose declared CID doesn't match the bytes is rejected by the
	// receiver — the client surfaces the error-ack.
	if err := hc.Stash("bafybeigdyrztktx5w5nykuoxkhhl6ulkc72sdzkyq3p7kdhhvzyipwlvke", bytes.NewReader(data)); err == nil {
		t.Fatal("expected a cid-mismatch push to be rejected")
	}

	// A reader that fails mid-stream surfaces as a streaming error.
	if err := hc.Stash("bafyx", &failingReader{ok: 2}); err == nil {
		t.Fatal("expected a mid-stream reader failure to error")
	}

	// Remote KVDB against the live hoarder (first-touch on this single node).
	kv, err := hc.KVDB(hoarderIface.Global, "kvproj", "", "/kv", "main")
	if err != nil {
		t.Fatalf("KVDB: %v", err)
	}
	kctx := context.Background()

	if err := kv.Put(kctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, err := kv.Get(kctx, "k1")
	if err != nil || string(v) != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if _, err := kv.Get(kctx, "missing"); err == nil {
		t.Fatal("Get of a missing key should error")
	}

	batch, err := kv.Batch(kctx)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	batch.Put("k2", []byte("v2")) //nolint:errcheck
	batch.Delete("k1")            //nolint:errcheck
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	keys, err := kv.List(kctx, "")
	if err != nil || len(keys) != 1 {
		t.Fatalf("List = %v, %v", keys, err)
	}
	if _, err := kv.ListRegEx(kctx, "", ".*"); err != nil {
		t.Fatalf("ListRegEx: %v", err)
	}
	if ch, err := kv.ListAsync(kctx, ""); err != nil {
		t.Fatalf("ListAsync: %v", err)
	} else {
		for range ch {
		}
	}
	if ch, err := kv.ListRegExAsync(kctx, "", ".*"); err != nil {
		t.Fatalf("ListRegExAsync: %v", err)
	} else {
		for range ch {
		}
	}
	if err := kv.Sync(kctx, "k2"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := kv.Delete(kctx, "k2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if kv.Stats(kctx) == nil {
		t.Fatal("Stats should be non-nil")
	}
	if kv.Factory() != nil {
		t.Fatal("remote Factory should be nil")
	}

	// Conditional write: only the first putnx of a key lands; a later one
	// reports existed and never overwrites.
	nx, ok := kv.(hoarderIface.NxKVDB)
	if !ok {
		t.Fatal("remote handle must support conditional writes")
	}
	if existed, err := nx.PutNx(kctx, "cond", []byte("first")); err != nil || existed {
		t.Fatalf("first PutNx = existed %v, %v", existed, err)
	}
	if existed, err := nx.PutNx(kctx, "cond", []byte("second")); err != nil || !existed {
		t.Fatalf("second PutNx = existed %v, %v", existed, err)
	}
	if v, err := kv.Get(kctx, "cond"); err != nil || string(v) != "first" {
		t.Fatalf("PutNx overwrote: %q, %v", v, err)
	}
	kv.Close()

	// Metas resolves the placement identity of a hash recorded by first-touch;
	// unknown hashes are omitted.
	kvHash := multihash.Hash("kvproj" + "" + "/kv")
	metas, err := hc.Metas(kvHash, "unknown-hash")
	if err != nil {
		t.Fatalf("Metas: %v", err)
	}
	if len(metas) != 1 || metas[0].Hash != kvHash ||
		metas[0].Kind != hoarderIface.Global || metas[0].Meta.ProjectId != "kvproj" || metas[0].Meta.Match != "/kv" {
		t.Fatalf("Metas = %+v", metas)
	}

	// StashStatus reports live claims for the stashed cid and the fleet target.
	claims, target, err := hc.StashStatus(cid, "unknown-cid")
	if err != nil {
		t.Fatalf("StashStatus: %v", err)
	}
	if target != 1 || claims[cid] != 1 || claims["unknown-cid"] != 0 {
		t.Fatalf("StashStatus = %v target %d", claims, target)
	}
}

// TestHoarderClient_BadAck points the client at a hoarder that accepts the push
// but replies with a malformed ack — the client must surface a decode error,
// not hang or panic.
func TestHoarderClient_BadAck(t *testing.T) {
	ctx := context.Background()

	hostile, err := peer.New(ctx, nil, keypair.NewRaw(), common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11016)}, nil, true, false)
	if err != nil {
		t.Fatalf("hostile peer: %v", err)
	}

	ss, err := streams.New(hostile, common.Hoarder, common.HoarderProtocol)
	if err != nil {
		t.Fatalf("stream service: %v", err)
	}
	err = ss.DefineStream(hoarderSpecs.StashCommand,
		func(context.Context, con.Connection, command.Body) (response.Response, error) {
			return response.Response{"ready": true}, nil
		},
		func(_ context.Context, rw io.ReadWriter) {
			io.Copy(io.Discard, rw)               //nolint:errcheck // drain header + bytes
			rw.Write([]byte("not a valid frame")) //nolint:errcheck // garbage ack
		})
	if err != nil {
		t.Fatalf("define stream: %v", err)
	}
	ss.Start()
	defer ss.Stop()

	peerC, err := peer.New(ctx, nil, keypair.NewRaw(), common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11018)}, nil, true, false)
	if err != nil {
		t.Fatalf("consumer peer: %v", err)
	}

	client, err := hoarder_client.New(ctx, peerC)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second)
	if err := peerC.Peer().Connect(ctx, peercore.AddrInfo{ID: hostile.ID(), Addrs: hostile.Peer().Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	time.Sleep(time.Second)

	if err := client.Peers(hostile.ID()).Stash("bafyx", bytes.NewReader([]byte("payload"))); err == nil {
		t.Fatal("expected a decode error from the malformed ack")
	}
}

// failingReader yields `ok` bytes then errors — to exercise the stream-copy
// error path.
type failingReader struct{ ok int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.ok <= 0 {
		return 0, fmt.Errorf("reader boom")
	}
	r.ok--
	p[0] = 'x'
	return 1, nil
}

// TestHoarderClient_Unreachable exercises the client's error paths when no
// hoarder is reachable — every call must return an error rather than hang.
func TestHoarderClient_Unreachable(t *testing.T) {
	ctx := context.Background()

	peerC, err := peer.New(ctx, nil, keypair.NewRaw(), common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11014)}, nil, true, false)
	if err != nil {
		t.Fatalf("new peer: %v", err)
	}

	client, err := hoarder_client.New(ctx, peerC)
	if err != nil {
		t.Fatal(err)
	}

	// A well-formed but unconnected peer ID → every request fails to reach it.
	bogus, err := peercore.Decode("12D3KooWQFwFDkkGnQ8y23wTUZ1kV3RVpZWkTgy5rd3jyvvV2ypM")
	if err != nil {
		t.Fatal(err)
	}
	hc := client.Peers(bogus)

	if err := hc.Stash("bafyx", bytes.NewReader([]byte("x"))); err == nil {
		t.Error("Stash to an unreachable peer should error")
	}
	if _, err := hc.Rare(); err == nil {
		t.Error("Rare against an unreachable peer should error")
	}
	if _, err := hc.List(); err == nil {
		t.Error("List against an unreachable peer should error")
	}
	if _, err := hc.ReplicasOf(hoarderIface.Global, "p", "", "m"); err == nil {
		t.Error("ReplicasOf against an unreachable peer should error")
	}
}
