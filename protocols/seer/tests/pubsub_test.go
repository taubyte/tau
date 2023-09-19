package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/v3/assert"
)

var (
	substrateCount = 3
	seerCount      = 3
)

// TODO: Revisit this test
func TestPubsub(t *testing.T) {
	t.Skip("something is quite broken with seer.ListNodes")
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"copies": seerCount}},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	time.Sleep(10 * time.Second)
	for i := 0; i < substrateCount; i++ {
		err = u.Service("substrate", &commonIface.ServiceConfig{})
		assert.NilError(t, err)
	}
	pids, err := u.GetServicePids("substrate")
	assert.NilError(t, err)
	assert.Equal(t, len(pids), substrateCount)

	// Give seer time to process all pubsub messages
	time.Sleep(20 * time.Second)

	seerIds, err := u.GetServicePids("seer")
	assert.NilError(t, err)
	assert.Equal(t, len(seerIds), seerCount)

	for _, id := range seerIds {
		seer, ok := u.SeerByPid(id)
		assert.Equal(t, ok, true)

		nodes, err := seer.ListNodes()
		assert.NilError(t, err)

		for _, id := range nodes {
			assert.Assert(t, len(id) > 0)
		}

		assert.Equal(t, len(nodes), substrateCount)
	}
}
