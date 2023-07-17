package service

import (
	"testing"

	_ "bitbucket.org/taubyte/auth/service"
	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	"bitbucket.org/taubyte/dreamland/services"
	_ "bitbucket.org/taubyte/hoarder/service"
	_ "bitbucket.org/taubyte/monkey/service"
	_ "bitbucket.org/taubyte/patrick/api/p2p"
	_ "bitbucket.org/taubyte/seer/service"
	_ "bitbucket.org/taubyte/tns/service"
	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/odo/protocols/billing/api/p2p"
)

func TestFixtureProvidesClients(t *testing.T) {
	u := services.Multiverse("fixtureProvidesClients")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"billing": {},
			"auth":    {},
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

	err = u.RunFixture("createProjectWithCustomer")
	if err == nil {
		t.Errorf("Expected to fail clients not found")
		return
	}
}

func TestFixtureProvidesServices(t *testing.T) {
	u := services.Multiverse("fixtureProvidesServices")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Auth:    &commonIface.ClientConfig{},
					Billing: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithCustomer")
	if err == nil {
		t.Errorf("Expected to fail services not found")
		return
	}
}

// FIXME: Fixture is broken
func TestDreamlandFixture(t *testing.T) {
	u := services.Multiverse("fixtureTest")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"billing": {},
			"auth":    {},
			"tns":     {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Billing: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
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

	err = u.RunFixture("createProjectWithCustomer")
	if err != nil {
		t.Errorf("Error with running fixture createProjectWithCustomer: %v", err)
		return
	}

	bids, err := simple.Billing().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(bids) != 1 {
		t.Errorf("Expected 1 customers to be registered got length %d", len(bids))
		return
	}

}
