package fixtures

import (
	"testing"

	"github.com/ipfs/go-log/v2"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/helpers"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

func TestAttachProdProject(t *testing.T) {
	log.SetLogLevel("seer.p2p.client", "PANIC")
	t.Skip("this project is not on prod anymore")

	m := dream.New(t.Context())
	defer m.Close()

	u := m.New(dream.UniverseConfig{Name: t.Name()})

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":    {},
			"tns":     {},
			"monkey":  {},
			"patrick": {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	err = u.RunFixture("setBranch", "dream")
	assert.NilError(t, err)

	err = u.RunFixture("attachProdProject", helpers.ProjectID, helpers.GitToken)
	assert.NilError(t, err)
}
