package service

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/tau/clients/p2p/patrick"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestFixtureProvidesClients(t *testing.T) {
	u := services.Multiverse(services.UniverseConfig{Name: "fixtureProvidesClients"})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {},
			"seer":    {},
			"patrick": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithJobs")
	if err == nil {
		t.Errorf("Expected to fail clients not found")
		return
	}
}

func TestFixtureProvidesServices(t *testing.T) {
	u := services.Multiverse(services.UniverseConfig{Name: "fixtureProvidesServices"})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Seer:    &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithJobs")
	if err == nil {
		t.Errorf("Expected to fail services not found")
		return
	}
}

func TestDreamlandFixture(t *testing.T) {
	u := services.Multiverse(services.UniverseConfig{Name: "fixtureTest"})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithJobs")
	if err != nil {
		t.Errorf("Error with running fixture createProjectWithJobs: %v", err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Check for 20 seconds after fixture is ran for the jobs
	attempts := 0
	for {
		attempts += 1

		jobs, err := simple.Patrick().List()
		if len(jobs) != 2 {
			err = fmt.Errorf("Expected 2 jobs got %d", len(jobs))
		}

		if err == nil {
			break
		}

		if attempts == 20 {
			t.Error(err)
			return
		}

		time.Sleep(1 * time.Second)
	}
}
