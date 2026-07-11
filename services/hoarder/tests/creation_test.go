//go:build dreaming

package tests

import (
	"bytes"
	"crypto/rand"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	slices "github.com/taubyte/tau/utils/slices/string"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	"github.com/taubyte/tau/core/services/hoarder"
)

func TestService_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {},
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

	cid1, err := addAndStashNewFile(u, hoarderClient)
	assert.NilError(t, err)

	cid2, err := addAndStashNewFile(u, hoarderClient)
	assert.NilError(t, err)

	// Re-stashing the same CID is idempotent (claim already held) and must not
	// error.
	f2, err := u.Hoarder().Node().GetFile(t.Context(), cid2)
	assert.NilError(t, err)
	err = hoarderClient.Stash(cid2, f2)
	f2.Close()
	assert.NilError(t, err)

	// Single hoarder → the replica target clamps to 1, so a lone claim already
	// meets it and nothing reads back as rare.
	rareCids, err := hoarderClient.Rare()
	assert.NilError(t, err)
	assert.Equal(t, len(rareCids), 0)

	// List returns the CIDs this hoarder claims.
	stashedCids, err := hoarderClient.List()
	assert.NilError(t, err)
	assert.Equal(t, len(stashedCids), 2)
	assert.Equal(t, slices.Contains(stashedCids, cid1), true)
	assert.Equal(t, slices.Contains(stashedCids, cid2), true)
}

// addAndStashNewFile creates a random file, imports it to the hoarder node to
// derive its CID, then pushes the bytes through the client (which claims it).
func addAndStashNewFile(u *dream.Universe, hoarderClient hoarder.Client) (cid string, err error) {
	data := make([]byte, 8)
	if _, err = rand.Read(data); err != nil {
		return
	}

	if cid, err = u.Hoarder().Node().AddFile(bytes.NewReader(data)); err != nil {
		return
	}

	err = hoarderClient.Stash(cid, bytes.NewReader(data))
	return
}
