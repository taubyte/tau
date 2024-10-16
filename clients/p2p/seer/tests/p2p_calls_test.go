package tests

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"

	seer "github.com/taubyte/tau/clients/p2p/seer"
)

func TestCalls(t *testing.T) {
	defaultInterval := seer.DefaultUsageBeaconInterval
	seer.DefaultUsageBeaconInterval = time.Millisecond * 100
	defer func() {
		seer.DefaultUsageBeaconInterval = defaultInterval
	}()

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"mock": 1}},
			"auth": {Others: map[string]int{"copies": 2}},
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

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second)

	seerClient, err := simple.Seer()
	if err != nil {
		t.Error(err)
		return
	}

	ids, err := seerClient.Usage().ListServiceId("auth")
	if err != nil {
		t.Error(err)
		return
	}

	serviceIds, err := ids.Get("ids")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println("serviceIds: ", serviceIds)

	serviceIds2, ok := serviceIds.([]interface{})
	if !ok {
		t.Errorf("serviceIds %#v is not []interface{}", nil)
		return
	}

	if len(serviceIds2) != 2 {
		t.Errorf("Expected 2 nodes got %d", len(serviceIds2))
	}

}
