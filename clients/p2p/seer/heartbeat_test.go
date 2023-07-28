package seer_test

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"

	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
)

func TestHeartBeat(t *testing.T) {
	defaultInterval := seerClient.DefaultUsageBeaconInterval
	seerClient.DefaultUsageBeaconInterval = time.Second
	defer func() {
		seerClient.DefaultUsageBeaconInterval = defaultInterval
	}()

	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: "TestHeartBeat"})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"substrate": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
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

	time.Sleep(5 * time.Second)

	ids, err := simple.Seer().Usage().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(ids) != 1 {
		t.Errorf("Expected 1 service, got %v", len(ids))
		return
	}

	serviceInfo, err := simple.Seer().Usage().Get(ids[0])
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("SERVICE INFO: %#v\n", serviceInfo)
}
