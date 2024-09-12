package auth_test

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"
)

func TestStats(t *testing.T) {
	t.TempDir()

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
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
