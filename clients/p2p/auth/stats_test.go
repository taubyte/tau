//go:build dreaming

package auth_test

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
)

func TestStats_Dreaming(t *testing.T) {
	t.TempDir()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	auth, err := simple.Auth()
	assert.NilError(t, err)

	// make sure database has a head
	injectCert(t, auth)

	stats, err := auth.Stats().Database()
	assert.NilError(t, err)

	assert.Equal(t, stats.Type(), kvdb.TypeCRDT)

	assert.Equal(t, len(stats.Heads()), 1)

}
