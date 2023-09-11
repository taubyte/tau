package service

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/libdream"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestFixtureProvidesClients(t *testing.T) {
	u := libdream.NewUniverse(libdream.UniverseConfig{Name: "fixtureProvidesClients"})
	defer u.Stop()

	err := u.StartWithConfig(&libdream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {},
			"seer":    {},
			"patrick": {},
		},
		Simples: map[string]libdream.SimpleConfig{
			"client": {
				Clients: libdream.SimpleConfigClients{}.Conform(),
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
	u := libdream.NewUniverse(libdream.UniverseConfig{Name: "fixtureProvidesServices"})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Seer:    &commonIface.ClientConfig{},
				}.Conform(),
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
	u := libdream.NewUniverse(libdream.UniverseConfig{Name: "fixtureTest"})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				}.Conform(),
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
			jobs, err := patrick.List()
			if err == nil && len(jobs) != 2 {
				err = fmt.Errorf("Expected 2 jobs got %d", len(jobs))
			}
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
