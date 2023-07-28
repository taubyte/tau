package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
)

func TestClearRepos(t *testing.T) {
	u := dreamland.Multiverse("TestImportProdProject")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{},
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

	err = u.RunFixture("clearRepos")
	if err != nil {
		t.Error(err)
		return
	}
}
