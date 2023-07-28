package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/helpers"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestAttachProdProject(t *testing.T) {
	u := dreamland.Multiverse("testrunlibrary")
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

	err = u.RunFixture("attachProdProject", helpers.ProjectID, helpers.GitToken, "dreamland")
	if err != nil {
		t.Error(err)
		return
	}

}
