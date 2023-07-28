package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestFakeProject(t *testing.T) {
	t.Skip("needs to be reimplemented")
	u := dreamland.Multiverse("TestFakeProject")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
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
