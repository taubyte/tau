//go:build dreaming

package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/dream"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// TestReplication_Dreaming drives placement end-to-end without substrate: a
// first write records a Global resource (skips TNS validation), HRW places it on
// the system-calculated number of hoarders, then killing one re-clamps the
// target to the surviving fleet — all in a few seconds, no auction.
func TestReplication_Dreaming(t *testing.T) {
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

	const (
		project = "QmReplicationTestProject"
		match   = "/replication/global"
	)

	// A first write records the resource; the client's cold-start retry rides out
	// mesh warmup, so no fixed sleep is needed.
	kv, err := hoarderClient.KVDB(hoarderIface.Global, project, "", match, "main")
	assert.NilError(t, err)
	assert.NilError(t, kv.Put(u.Context(), "seed", []byte("seed")))

	// Fleet of 3, DefaultReplicaTarget 3 → HRW places all three hoarders.
	got := waitForReplicas(t, hoarderClient, project, match, 3, 90*time.Second)
	assert.Equal(t, got, 3, "expected 3 live replicas after placement")

	// Kill one hoarder: membership drops it, HRW re-clamps to the live fleet (2),
	// and reconcile settles there without thrashing.
	pids, err := u.GetServicePids("hoarder")
	assert.NilError(t, err)
	assert.Assert(t, len(pids) >= 1)
	assert.NilError(t, u.KillNodeByNameID("hoarder", pids[0]))

	got = waitForReplicas(t, hoarderClient, project, match, 2, 90*time.Second)
	assert.Equal(t, got, 2, "expected replicas to re-clamp to the surviving fleet of 2")
}

// fastConvergence shrinks the heartbeat/liveness/backstop cadences so membership
// and placement converge in test time (production defaults trade a little
// reactivity for less chatter). Restored on cleanup.
func fastConvergence(t *testing.T) {
	t.Helper()
	origHB := hoarderSpecs.HeartbeatInterval
	origLive := hoarderSpecs.LivenessTimeout
	origBackstop := hoarderSpecs.ReconcileBackstop
	origJitter := hoarderSpecs.ReconcileJitter
	hoarderSpecs.HeartbeatInterval = 500 * time.Millisecond
	hoarderSpecs.LivenessTimeout = 5 * time.Second
	hoarderSpecs.ReconcileBackstop = 3 * time.Second
	hoarderSpecs.ReconcileJitter = 100 * time.Millisecond
	t.Cleanup(func() {
		hoarderSpecs.HeartbeatInterval = origHB
		hoarderSpecs.LivenessTimeout = origLive
		hoarderSpecs.ReconcileBackstop = origBackstop
		hoarderSpecs.ReconcileJitter = origJitter
	})
}

// waitForReplicas polls ReplicasOf until the live replica count equals want or
// the timeout elapses, returning the last observed count.
func waitForReplicas(t *testing.T, client hoarderIface.Client, project, match string, want int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		peers, err := client.ReplicasOf(hoarderIface.Global, project, "", match)
		if err == nil {
			last = len(peers)
			if last == want {
				return last
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return last
}
