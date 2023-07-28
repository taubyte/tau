package tests

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/substrate"
)

func TestCalls(t *testing.T) {
	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: "p2pCalls"})
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"dns": protocolsCommon.DefaultDevDnsPort, "mock": 1}},
			"substrate": {Others: map[string]int{"copies": 2}},
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

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second)

	ids, err := simple.Seer().Usage().ListServiceId("substrate")
	if err != nil {
		t.Error(err)
		return
	}

	serviceIds, err := ids.Get("ids")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println("IDSSS ", serviceIds)

	serviceIds2 := serviceIds.([]interface{})

	if len(serviceIds2) != 2 {
		t.Errorf("Expected 2 nodes got %d", len(serviceIds2))
	}

}
