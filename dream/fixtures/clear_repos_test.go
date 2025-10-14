package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
)

func TestClearRepos(t *testing.T) {
	t.Skip("Needs to be redone")

	m := dream.New(t.Context())
	defer m.Close()

	u := m.New(dream.UniverseConfig{Name: t.Name()})

	err := u.StartWithConfig(&dream.Config{
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
