//go:build dreaming

package tests

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// TestStashFanout_Dreaming pushes a blob with a replica target of 3 to a
// 3-hoarder fleet and asserts the receiving hoarder fans the bytes out to its
// co-claimants — so the CID reaches the target and stops reading as rare.
func TestStashFanout_Dreaming(t *testing.T) {
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

	// Let the fleet form so fan-out can find co-claimants.

	data := make([]byte, 32)
	_, err = rand.Read(data)
	assert.NilError(t, err)

	cid, err := u.Hoarder().Node().AddFile(bytes.NewReader(data))
	assert.NilError(t, err)

	// Push with a replica target of 3 → the receiver fans out to two others.
	err = hoarderClient.Stash(cid, bytes.NewReader(data), hoarderIface.WithTarget(3))
	assert.NilError(t, err)

	// Poll for convergence rather than sampling once. Rare() and List() are both
	// per-node views over the eventually-consistent CRDT stash registry, and the
	// client's RPCs may land on any fleet node — so Rare() can read "not rare"
	// from a node before the node List() hits has finished claiming via fan-out.
	// The end state is stable (target=3=fleet → every node claims the CID), so
	// wait for both to hold together: replicated to target (no longer rare) AND
	// the queried hoarder lists its own claim.
	converged := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		rare, err := hoarderClient.Rare()
		assert.NilError(t, err)
		list, err := hoarderClient.List()
		assert.NilError(t, err)
		if !containsCid(rare, cid) && containsCid(list, cid) {
			converged = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	assert.Assert(t, converged, "CID should replicate to target and stay claimed by the receiving hoarder")
}

func containsCid(cids []string, cid string) bool {
	for _, c := range cids {
		if c == cid {
			return true
		}
	}
	return false
}
