package service

import (
	"testing"
	"time"

	_ "github.com/taubyte/tau/clients/p2p/patrick"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/tns"
)

func TestFixtureProvidesClients(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: "fixtureProvidesClients"})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {},
			"seer":    {},
			"patrick": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{}.Compat(),
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
	u := dream.New(dream.UniverseConfig{Name: "fixtureProvidesServices"})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Seer:    &commonIface.ClientConfig{},
				}.Compat(),
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
	t.Skip("Using an old token/project")
	u := dream.New(dream.UniverseConfig{Name: "fixtureTest"})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				}.Compat(),
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

		patrick, err := simple.Patrick()
		if err != nil {
			t.Error(err)
			return
		}

		jobs, err := patrick.List()
		if err != nil {
			t.Error(err)
			return
		}

		if len(jobs) != 2 {
			t.Errorf("Expected 2 jobs got %d", len(jobs))
			return
		}

		if attempts == 20 {
			t.Error(err)
			return
		}

		time.Sleep(1 * time.Second)
	}
}
