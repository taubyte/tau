package seer_test

import (
	"fmt"
	"testing"
	"time"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/seer"
)

func TestHeartBeat(t *testing.T) {
	defaultInterval := seerClient.DefaultUsageBeaconInterval
	seerClient.DefaultUsageBeaconInterval = time.Second
	defer func() {
		seerClient.DefaultUsageBeaconInterval = defaultInterval
	}()

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {},
			"auth": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
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

	time.Sleep(10 * time.Second)
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
