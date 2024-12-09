package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	cliCommon "github.com/taubyte/tau/tools/dream/cli/common"

	client "github.com/taubyte/tau/clients/http/dream"
	commonIface "github.com/taubyte/tau/core/common"

	_ "github.com/taubyte/tau/utils/dream"
)

var services = []string{"seer", "auth", "patrick", "tns", "monkey", "hoarder", "substrate"}

func TestKillService(t *testing.T) {
	api.BigBang()
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples:  map[string]dream.SimpleConfig{},
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = u.Service("tns", &commonIface.ServiceConfig{})
	if err != nil {
		t.Error(err)
		return
	}

	tnsIds, err := u.GetServicePids("tns")
	if err != nil {
		t.Error(err)
		return
	}
	idToDelete := tnsIds[0]

	err = u.KillNodeByNameID("tns", idToDelete)
	if err != nil {
		t.Error(err)
		return
	}

	tnsIds, err = u.GetServicePids("tns")
	if err != nil {
		t.Error(err)
		return
	}

	result := len(tnsIds)
	if result == 1 || result > 1 {
		t.Errorf("Service was not deleted with id: %s", idToDelete)
		return
	}

	multiverse, err := client.New(u.Context(), client.URL(cliCommon.DefaultDreamlandURL), client.Timeout(300*time.Second))
	if err != nil {
		t.Error(err)
		return
	}

	resp, err := multiverse.Universe(t.Name()).Chart()
	if err != nil {
		t.Error(err)
		return
	}

	if len(resp.Nodes) != 1 {
		t.Errorf("Service was not deleted with id: %s", idToDelete)
		return
	}
}

func TestKillSimple(t *testing.T) {
	testSimpleName := "client"
	universeName := "KillSimple"
	statusName := fmt.Sprintf("%s@%s", testSimpleName, universeName)

	api.BigBang()
	u := dream.New(dream.UniverseConfig{Name: universeName})
	err := u.StartWithConfig(&dream.Config{
		Simples: map[string]dream.SimpleConfig{
			testSimpleName: {},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}
	multiverse, err := client.New(u.Context(), client.URL(cliCommon.DefaultDreamlandURL), client.Timeout(1000*time.Second))
	if err != nil {
		t.Error(err)
		return
	}
	universeAPI := multiverse.Universe(universeName)

	simple, err := u.Simple(testSimpleName)
	if err != nil {
		t.Error(err)
		return
	}

	resp, err := universeAPI.Chart()
	if err != nil {
		t.Error(err)
		return
	}
	var found bool
	for _, node := range resp.Nodes {
		if node.Name == statusName {
			found = true
		}
	}
	if found == false {
		t.Errorf("Couldn't find simple %s", testSimpleName)
		return
	}

	err = u.KillNodeByNameID("client", simple.PeerNode().ID().String())
	if err != nil {
		t.Error(err)
		return
	}

	_, err = u.Simple("client")
	if err == nil {
		t.Error("Expected an error")
		return
	}

	resp, err = universeAPI.Chart()
	if err != nil {
		t.Error(err)
		return
	}
	found = false
	for _, node := range resp.Nodes {
		if node.Name == statusName {
			found = true
		}
	}
	if found == true {
		t.Errorf("Found simple: %s when it should have been deleted", testSimpleName)
		return
	}

	// Create another with same name
	_, err = u.CreateSimpleNode("client", &dream.SimpleConfig{
		CommonConfig: commonIface.CommonConfig{},
	})
	if err != nil {
		t.Error(err)
		return
	}

	resp, err = universeAPI.Chart()
	if err != nil {
		t.Error(err)
		return
	}
	found = false
	for _, node := range resp.Nodes {
		if node.Name == statusName {
			found = true
		}
	}
	if found != true {
		t.Errorf("Couldn't find simple %s after recreating", testSimpleName)
		return
	}
}

func TestMultipleServices(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"copies": 1}},
			"auth":      {Others: map[string]int{"copies": 3}},
			"patrick":   {Others: map[string]int{"copies": 3}},
			"tns":       {Others: map[string]int{"copies": 3}},
			"monkey":    {Others: map[string]int{"copies": 3}},
			"hoarder":   {Others: map[string]int{"copies": 3}},
			"substrate": {Others: map[string]int{"copies": 3}},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	for _, v := range services {
		if u.ListNumber(v) != 3 && v != "seer" {
			t.Errorf("Service %s does not have 2 copies got %d", v, u.ListNumber(v))
			return
		}
	}

	time.Sleep(time.Second * 1)
}
