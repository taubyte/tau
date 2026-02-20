//go:build dreaming

package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"
)

func TestClearRepos_Dreaming(t *testing.T) {
	t.Skip("Needs to be redone")

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{}.Compat(),
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
