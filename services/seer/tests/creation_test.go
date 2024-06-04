package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/seer"
)

func TestService(t *testing.T) {
	fake_location := iface.Location{Latitude: 32.91264411258042, Longitude: -96.8907727708027}
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
					TNS:  &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	// give time for peers to discover each other
	time.Sleep(1 * time.Second)

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	seer, err := simple.Seer()
	assert.NilError(t, err)

	err = seer.Geo().Set(fake_location)
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	time.Sleep(1 * time.Second)

	resp, err := seer.Geo().All()
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	found_match := false
	for _, p := range resp {
		if p.Id == simple.PeerNode().ID().String() {
			if p.Location.Location == fake_location {
				found_match = true
			}
		}
	}
	if !found_match {
		t.Error("Can't find peer location in All() query")
		return
	}
}
