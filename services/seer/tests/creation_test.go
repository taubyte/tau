//go:build dreaming

package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

func TestService_Dreaming(t *testing.T) {
	fake_location := iface.Location{Latitude: 32.91264411258042, Longitude: -96.8907727708027}

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	seer, err := simple.Seer()
	assert.NilError(t, err)

	// give time for peers to discover each other
	for deadline := time.Now().Add(2 * time.Second); ; {
		err = seer.Geo().Set(fake_location)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.NilError(t, err)

	// the just-set location reaches the other seer copies via pubsub gossip —
	// poll All() until it shows up rather than guessing a fixed delay.
	var resp []*iface.Peer
	found_match := false
	for deadline := time.Now().Add(2 * time.Second); ; {
		resp, err = seer.Geo().All()
		assert.NilError(t, err)

		for _, p := range resp {
			if p.Id == simple.PeerNode().ID().String() && p.Location.Location == fake_location {
				found_match = true
				break
			}
		}

		if found_match || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Assert(t, found_match, "Can't find peer location in All() query")
}
