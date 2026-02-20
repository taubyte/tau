//go:build dreaming

package patrick_test

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
)

func TestStats_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	pat, err := simple.Patrick()
	assert.NilError(t, err)

	stats, err := pat.DatabaseStats()
	assert.NilError(t, err)

	assert.Equal(t, stats.Type(), kvdb.TypeCRDT)

	assert.Equal(t, len(stats.Heads()), 0)
}
