package tns

import (
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

func TestDreamlandDoubleClient(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: "single"})
	defer dream.Zeno()

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
			"client2": {
				Clients: dream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}
	defer u.Stop()

	testKey := []string{"orange"}
	testValue := "someOrange"

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	simple2, err := u.Simple("client2")
	assert.NilError(t, err)

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	err = tnsClient.Push(testKey, testValue)
	assert.NilError(t, err)

	tnsClient2, err := simple2.TNS()
	assert.NilError(t, err)

	val, err := tnsClient2.Fetch(spec.NewTnsPath(testKey))
	assert.NilError(t, err)

	assert.DeepEqual(t, val.Interface(), testValue)
}
