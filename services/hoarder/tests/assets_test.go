//go:build dreaming

package tests

import (
	"bytes"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/pkg/specs/methods"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

// TestAssetSweep_Dreaming: an asset CID recorded in TNS whose bytes sit
// un-replicated on a non-hoarder node (no stash claims — the shape a failed or
// pre-data-plane build leaves behind) is adopted by the recurring sweep:
// fetched over bitswap, pinned, claimed to the stash target.
func TestAssetSweep_Dreaming(t *testing.T) {
	fastConvergence(t)
	origInterval := hoarderSpecs.AssetSweepInterval
	origRetry := hoarderSpecs.AssetSweepRetryInterval
	hoarderSpecs.AssetSweepInterval = 3 * time.Second
	hoarderSpecs.AssetSweepRetryInterval = 2 * time.Second
	t.Cleanup(func() {
		hoarderSpecs.AssetSweepInterval = origInterval
		hoarderSpecs.AssetSweepRetryInterval = origRetry
	})

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"tns":     {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {Clients: dream.SimpleConfigClients{
				TNS:     &commonIface.ClientConfig{},
				Hoarder: &commonIface.ClientConfig{},
			}.Compat()},
		},
	}))
	u.Mesh()

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	// The bytes live only on the client node — no stash claims anywhere.
	data := bytes.Repeat([]byte("built artifact "), 128)
	cid, err := simple.PeerNode().AddFile(bytes.NewReader(data))
	assert.NilError(t, err)

	// Record the asset in TNS the way a build does.
	tns, err := simple.TNS()
	assert.NilError(t, err)
	assetKey, err := methods.GetTNSAssetPath("someproject", "someresource", "main")
	assert.NilError(t, err)
	assert.NilError(t, tns.Push(assetKey.Slice(), cid))

	// The recurring sweep must adopt it: claims reach the fleet target.
	hoarder, err := simple.Hoarder()
	assert.NilError(t, err)
	deadline := time.Now().Add(90 * time.Second)
	for {
		claims, target, err := hoarder.StashStatus(cid)
		if err == nil && claims[cid] >= target && target >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("asset never adopted: claims=%v err=%v", claims, err)
		}
		time.Sleep(time.Second)
	}

	// The hoarder holds the bytes locally now (not just a claim).
	f, err := u.Hoarder().Node().GetFile(u.Context(), cid)
	assert.NilError(t, err)
	got := make([]byte, len(data))
	_, err = f.Read(got)
	f.Close()
	assert.NilError(t, err)
	assert.DeepEqual(t, got, data)
}
