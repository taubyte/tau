package tns_test

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"
)

func TestStats(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	stats, err := tns.Stats().Database()
	assert.NilError(t, err)

	assert.Equal(t, stats.Type(), kvdb.TypeCRDT)

	assert.Equal(t, len(stats.Heads()), 0)

}
