package seer_test

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
)

func TestHeartBeat(t *testing.T) {
	defaultInterval := seerClient.DefaultUsageBeaconInterval
	seerClient.DefaultUsageBeaconInterval = time.Second
	defer func() {
		seerClient.DefaultUsageBeaconInterval = defaultInterval
	}()

	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"substrate": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
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
	seer, err := simple.Seer()
	assert.NilError(t, err)

	ids, err := seer.Usage().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(ids) != 1 {
		t.Errorf("Expected 1 service, got %v", len(ids))
		return
	}

	serviceInfo, err := seer.Usage().Get(ids[0])
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("SERVICE INFO: %#v\n", serviceInfo)
}
