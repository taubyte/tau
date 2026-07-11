//go:build dreaming

package tests

import (
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/tau/core/common"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// TestKVDB_Dreaming exercises the remote data plane end-to-end: first-touch on
// an unplaced Global instance, then put/get/delete/list through the remote
// kvdb.KVDB client.
func TestKVDB_Dreaming(t *testing.T) {
	fastConvergence(t)

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {Others: map[string]int{"copies": 3}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer:    &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	kv, err := hoarderClient.KVDB(hoarderIface.Global, "kvproj", "", "/kv/instance", "main")
	assert.NilError(t, err)

	ctx := u.Context()

	// First-touch: a put to an unplaced instance claims + loads it on some
	// hoarder, then serves the write.
	assert.NilError(t, kv.Put(ctx, "alpha", []byte("one")))
	assert.NilError(t, kv.Put(ctx, "beta", []byte("two")))

	got, err := kv.Get(ctx, "alpha")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "one")

	keys, err := kv.List(ctx, "")
	assert.NilError(t, err)
	assert.Equal(t, len(keys), 2)

	// Batch put + delete.
	batch, err := kv.Batch(ctx)
	assert.NilError(t, err)
	assert.NilError(t, batch.Put("gamma", []byte("three")))
	assert.NilError(t, batch.Delete("beta"))
	assert.NilError(t, batch.Commit())

	got, err = kv.Get(ctx, "gamma")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "three")

	_, err = kv.Get(ctx, "beta")
	assert.ErrorContains(t, err, "not found")

	assert.NilError(t, kv.Delete(ctx, "alpha"))
	_, err = kv.Get(ctx, "alpha")
	assert.ErrorContains(t, err, "not found")

	// Remaining remote KVDB surface (gamma still present).
	assert.NilError(t, kv.Sync(ctx, "gamma"))

	regKeys, err := kv.ListRegEx(ctx, "", ".*")
	assert.NilError(t, err)
	assert.Assert(t, len(regKeys) >= 1)

	ch, err := kv.ListAsync(ctx, "")
	assert.NilError(t, err)
	async := 0
	for range ch {
		async++
	}
	assert.Assert(t, async >= 1)

	rch, err := kv.ListRegExAsync(ctx, "", ".*")
	assert.NilError(t, err)
	for range rch {
	}

	assert.Assert(t, kv.Stats(ctx) != nil)
	assert.Assert(t, kv.Factory() == nil)
	kv.Close()
}

// TestKVDBDurability_Dreaming is the keystone: a write acked after the K=2
// barrier must survive the death of the replica that served it — readable via a
// surviving co-claimant.
func TestKVDBDurability_Dreaming(t *testing.T) {
	fastConvergence(t)

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	// Exactly two copies: with target=2 the K=2 barrier places the durable write
	// on BOTH claimants synchronously, so killing one leaves a survivor that holds
	// it directly (no dependence on async CRDT catch-up to a third replica). This
	// is the faithful durability proof — K=2 acked ⇒ survives one death.
	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer:    &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	const (
		project = "durproj"
		match   = "/dur/instance"
	)
	kv, err := hoarderClient.KVDB(hoarderIface.Global, project, "", match, "main")
	assert.NilError(t, err)
	ctx := u.Context()

	// First write triggers first-touch + a background auction for the rest.
	assert.NilError(t, kv.Put(ctx, "seed", []byte("seed")))

	// Wait until at least two replicas hold the instance, so the K=2 barrier can
	// place the durable write on a second node.
	var replicas []peercore.ID
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		replicas, err = hoarderClient.ReplicasOf(hoarderIface.Global, project, "", match)
		if err == nil && len(replicas) >= 2 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	assert.Assert(t, len(replicas) >= 2, "need >=2 replicas for the K=2 barrier")

	// This write is acked only after a co-claimant persisted it.
	assert.NilError(t, kv.Put(ctx, "durable", []byte("survivor-value")))

	// Kill one replica (the client fails over if it was the sticky one).
	assert.NilError(t, u.KillNodeByNameID("hoarder", replicas[0].String()))

	// Read back via a survivor. Failover may first hit a claimant that only has
	// the value via async CRDT catch-up, so poll until it converges.
	var got []byte
	found := false
	readDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(readDeadline) {
		got, err = kv.Get(ctx, "durable")
		if err == nil && string(got) == "survivor-value" {
			found = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	assert.Assert(t, found, "durable write must survive the replica's death (got %q, err %v)", got, err)
}
