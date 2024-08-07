package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/tns"
)

func TestImportProdProject(t *testing.T) {
	t.Skip("currently custom domains do not work on dreamland")

	spec.DefaultBranches = []string{"master_test"}
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

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
