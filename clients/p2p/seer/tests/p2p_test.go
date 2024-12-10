package tests

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/services/seer"
	"gotest.tools/v3/assert"
)

func TestSeerClient(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"dns": 8988}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
			},
			"clientD": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	// Error reporting no peers providing but we are checking if its 0 so just not returning
	seer, err := simple.Seer()
	assert.NilError(t, err)

	resp, err := seer.Geo().All()
	assert.NilError(t, err)

	if len(resp) != 0 {
		t.Error("Should return empty! returned:", resp)
		return
	}

	/***** SET *****/

	// location of office in 12100 Ford Rd
	fake_location := iface.Location{Latitude: 32.91264411258042, Longitude: -96.8907727708027}
	err = seer.Geo().Set(fake_location)
	assert.NilError(t, err)

	/***** ALL *****/

	resp, err = seer.Geo().All()
	assert.NilError(t, err)

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

	/***** QUERY BY DISTANCE *****/

	// DFW airport
	fake_now_location := iface.Location{Latitude: 32.900211956131386, Longitude: -97.04029425876429}

	_, err = seer.Geo().Distance(fake_now_location, 15*1000)
	assert.NilError(t, err)

	_, err = seer.Geo().Distance(fake_now_location, 5*1000)
	assert.NilError(t, err)
}
