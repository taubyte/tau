package tests

import (
	"testing"
	"time"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
)

var (
	nodeCount = 3
)

// TODO: Revisit this test
func TestPubsub(t *testing.T) {
	t.Skip("this test needs to be redone")
	u := dreamland.Multiverse("seerDNS_test")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"copies": 3}},
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

	// Time for all seers to startup
	time.Sleep(10 * time.Second)

	for i := 0; i < nodeCount; i++ {
		err = u.Service("node", &commonIface.ServiceConfig{})
		if err != nil {
			t.Error(err)
			return
		}
	}

	// Give seer time to process all pubsub messages
	time.Sleep(20 * time.Second)

	seerIds, err := u.GetServicePids("seer")
	if err != nil {
		t.Error(err)
		return
	}

	for _, id := range seerIds {
		seer, ok := u.SeerByPid(id)
		if ok == false {
			t.Errorf("Seer %s not found", id)
			return
		}

		nodes, err := seer.ListNodes()
		if err != nil {
			t.Error(err)
			return
		}

		for _, id := range nodes {
			if id == "" {
				t.Error("Id is nil")
				return
			}
		}

		if len(nodes) != nodeCount {
			t.Errorf("\n Did not get correct number of node id's for seer %s. \n %d != %d \n List of Node Id's %v", id, len(nodes), nodeCount, nodes)
			return
		}

	}
}
