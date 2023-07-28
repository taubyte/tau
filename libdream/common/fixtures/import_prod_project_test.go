package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/helpers"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestImportProdProject(t *testing.T) {
	t.Skip("currently custom domains do not work on dreamland")

	spec.DefaultBranch = "master_test"

	u := dreamland.Multiverse("TestImportProdProject")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":    {},
			"tns":     {},
			"monkey":  {},
			"patrick": {},
			"hoarder": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("importProdProject", "QmYfMsCDvC9geoRRMCwRxvW1XSn3VQQoevBC48D9scmLJX", helpers.GitToken, "master_test")
	if err != nil {
		t.Error(err)
		return
	}
}
