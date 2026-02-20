//go:build dreaming

package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	cliCommon "github.com/taubyte/tau/tools/dream/cli/common"
	"gotest.tools/v3/assert"

	client "github.com/taubyte/tau/clients/http/dream"
	commonIface "github.com/taubyte/tau/core/common"

	_ "github.com/taubyte/tau/utils/dream"
)

var services = []string{"seer", "auth", "patrick", "tns", "monkey", "hoarder", "substrate"}

func init() {
	dream.DreamApiPort = 41421 // don't conflict with default port
}

func TestKillService_Dreaming(t *testing.T) {
	dream.DreamApiPort = 43421
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()
	assert.NilError(t, api.BigBang(m))

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
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
	assert.NilError(t, err)
	assert.Assert(t, len(tnsIds) > 0)
	idToDelete := tnsIds[0]

	err = u.KillNodeByNameID("tns", idToDelete)
	assert.NilError(t, err)

	tnsIds, err = u.GetServicePids("tns")
	assert.NilError(t, err)
	assert.Equal(t, len(tnsIds), 0)

	multiverse, err := client.New(u.Context(), client.URL(cliCommon.DefaultDreamURL()), client.Timeout(300*time.Second))
	assert.NilError(t, err)

	resp, err := multiverse.Universe(t.Name()).Chart()
	assert.NilError(t, err)
	assert.Equal(t, len(resp.Nodes), 0)

}

func TestKillSimple_Dreaming(t *testing.T) {
	testSimpleName := "client"
	universeName := "killsimple"
	statusName := fmt.Sprintf("%s@%s", testSimpleName, universeName)

	dream.DreamApiPort = 40424
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()
	assert.NilError(t, api.BigBang(m))

	u, err := m.New(dream.UniverseConfig{Name: universeName})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Simples: map[string]dream.SimpleConfig{
			testSimpleName: {},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}
	multiverse, err := client.New(u.Context(), client.URL(cliCommon.DefaultDreamURL()), client.Timeout(1000*time.Second))
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
		t.Logf("Node: %+v", node)
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

func TestMultipleServices_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
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
