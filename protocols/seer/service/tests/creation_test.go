package tests

import (
	"fmt"
	"testing"
	"time"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	iface "github.com/taubyte/go-interfaces/services/seer"

	_ "github.com/taubyte/odo/protocols/seer/service"
)

func TestService(t *testing.T) {
	fake_location := iface.Location{Latitude: 32.91264411258042, Longitude: -96.8907727708027}
	u := dreamland.Multiverse("seerService")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"dns": 8999}},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
					TNS:  &commonIface.ClientConfig{},
				},
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

	err = simple.Seer().Geo().Set(fake_location)
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	resp, err := simple.Seer().Geo().All()
	if err != nil {
		t.Error("Returned Error ", err)
		return
	}

	found_match := false
	for _, p := range resp {
		if p.Id == simple.GetNode().ID().Pretty() {
			fmt.Println(p.Location.Location)
			if p.Location.Location == fake_location {
				found_match = true
			}
		}
	}
	if found_match == false {
		t.Error("Can't find peer location in All() query")
		return
	}
}
