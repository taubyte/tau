package http

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/clients/http/dream/inject"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	_ "github.com/taubyte/tau/services/auth"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/tns"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/clients/p2p/patrick"
	_ "github.com/taubyte/tau/clients/p2p/tns"
)

func TestRoutes(t *testing.T) {
	univerName := "dreamland-http"
	// start multiverse
	err := api.BigBang()
	assert.NilError(t, err)

	u := dream.New(dream.UniverseConfig{Name: univerName})
	defer u.Stop()

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"auth":    {},
			"patrick": {},
			"seer":    {},
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Monkey:  &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					TNS:     &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	ctx := context.Background()

	time.Sleep(2 * time.Second)

	client, err := New(ctx, URL("http://localhost:1421"), Timeout(60*time.Second))
	assert.NilError(t, err)

	univs, err := client.Universes()
	assert.NilError(t, err)

	assert.DeepEqual(t, univs[univerName].SwarmKey, u.SwarmKey())

	assert.Equal(t, univs[univerName].NodeCount, 8)

	universe := client.Universe(univerName)

	// Create simple called test1
	err = universe.Inject(inject.Simple("test1", &dream.SimpleConfig{}))
	if err != nil {
		t.Errorf("Failed simples call with error: %v", err)
		return
	}

	time.Sleep(2 * time.Second)

	// Should not fail
	_, err = u.Simple("test1")
	if err != nil {
		t.Errorf("Failed getting simple with error: %v", err)
		return
	}

	// Should fail
	_, err = u.Simple("dne")
	if err == nil {
		t.Error("Should have failed, expecting to not find dne simple node")
		return
	}

	// Should fail
	err = universe.Inject(inject.Fixture("should fail", "dne"))
	if err == nil {
		t.Error("Expecting fail for fixture not existing")
		return
	}

	test, err := client.Status()
	if err != nil {
		t.Error(err)
		return
	}
	_, ok := test[univerName]
	if ok == false {
		t.Error("Did not find universe in status")
		return
	}

}
