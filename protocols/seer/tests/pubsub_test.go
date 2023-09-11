package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
)

var (
	nodeCount = 3
)

// TODO: Revisit this test
func TestPubsub(t *testing.T) {
	t.Skip("this test needs to be redone")
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"copies": 3}},
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

	time.Sleep(10 * time.Second)
	for i := 0; i < nodeCount; i++ {
		err = u.Service("substrate", &commonIface.ServiceConfig{})
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
