package tests

import (
	"testing"
	"time"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	protocolsCommon "github.com/taubyte/odo/protocols/common"

	_ "github.com/taubyte/odo/protocols/auth/service"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
	_ "github.com/taubyte/odo/protocols/monkey/service"
	_ "github.com/taubyte/odo/protocols/node/service"
	_ "github.com/taubyte/odo/protocols/patrick/service"
)

func TestCalls(t *testing.T) {
	u := dreamland.Multiverse("p2pCalls")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"dns": protocolsCommon.DefaultDevDnsPort, "mock": 1}},
			"node": {Others: map[string]int{"copies": 2}},
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

	ids, err := simple.Seer().Usage().ListServiceId("node")
	if err != nil {
		t.Error(err)
		return
	}

	serviceIds, err := ids.Get("ids")
	if err != nil {
		t.Error(err)
		return
	}

	serviceIds2 := serviceIds.([]interface{})

	if len(serviceIds2) != 2 {
		t.Errorf("Expected 2 nodes got %d", len(serviceIds2))
	}

}
