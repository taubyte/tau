package fixtures

import (
	"testing"

	"github.com/ipfs/go-log/v2"
	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/helpers"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/tns"
	"gotest.tools/v3/assert"
)

func TestAttachProdProject(t *testing.T) {
	log.SetLogLevel("seer.p2p.client", "PANIC")
	t.Skip("this project is not on prod anymore")

	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":    {},
			"tns":     {},
			"monkey":  {},
			"patrick": {},
			"hoarder": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				},
			},
		},
	})
	assert.NilError(t, err)

	err = u.RunFixture("setBranch", "dreamland")
	assert.NilError(t, err)

	err = u.RunFixture("attachProdProject", helpers.ProjectID, helpers.GitToken)
	assert.NilError(t, err)
}
