package tests

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	iface "github.com/taubyte/go-interfaces/services/seer"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/seer"
)

func TestSeerClient(t *testing.T) {
	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"dns": 8988}},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				},
			},
			"clientD": {
				Clients: commonDreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Error reporting no peers providing but we are checking if its 0 so just not returning
	resp, err := simple.Seer().Geo().All()
	if err != nil {
		t.Error("Seer geo all err: ", err)
	}

	if len(resp) != 0 {
		t.Error("Should return empty! returned:", resp)
		return
	}

	/***** SET *****/

	// location of office in 12100 Ford Rd
	fake_location := iface.Location{Latitude: 32.91264411258042, Longitude: -96.8907727708027}
	err = simple.Seer().Geo().Set(fake_location)
	if err != nil {
		t.Error("Geo set: ", err)
		return
	}

	/***** ALL *****/

	resp, err = simple.Seer().Geo().All()
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	found_match := false
	for _, p := range resp {
		if p.Id == simple.PeerNode().ID().Pretty() {
			if p.Location.Location == fake_location {
				found_match = true
			}
		}
	}
	if found_match == false {
		t.Error("Can't find peer location in All() query")
		return
	}

	/***** QUERY BY DISTANCE *****/

	// DFW airport
	fake_now_location := iface.Location{Latitude: 32.900211956131386, Longitude: -97.04029425876429}

	_, err = simple.Seer().Geo().Distance(fake_now_location, 15*1000)
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	_, err = simple.Seer().Geo().Distance(fake_now_location, 5*1000)
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

}
