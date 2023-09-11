package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestFakeProject(t *testing.T) {
	t.Skip("needs to be reimplemented")
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Conform(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("fakeProject")
	if err != nil {
		t.Error(err)
		return
	}
}
