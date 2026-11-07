package dream_test

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

// A Dream restart must bring a persisted universe back running, with the same
// identity and the data its services stored.
func TestPersistentUniverseComesBackRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m, err := dream.New(t.Context(), dream.Name("persist"))
	assert.NilError(t, err)

	u, err := m.New(dream.UniverseConfig{Name: "keep", KeepRoot: true})
	assert.NilError(t, err)
	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{"tns": {}},
	}))

	assert.NilError(t, u.TNS().KV().Put(t.Context(), "/kept", []byte("value")))
	pids, err := u.GetServicePids("tns")
	assert.NilError(t, err)
	swarmKey := m.Status()["keep"].SwarmKey
	httpPort, err := u.GetPortHttp(u.TNS().Node())
	assert.NilError(t, err)

	assert.NilError(t, m.Close())

	m2, err := dream.New(t.Context(), dream.LoadPersistent(), dream.Name("persist"))
	assert.NilError(t, err)
	defer m2.Close()

	u2, err := m2.Universe("keep")
	assert.NilError(t, err)
	assert.Assert(t, u2.Running(), "persisted universe was loaded but not started")

	pids2, err := u2.GetServicePids("tns")
	assert.NilError(t, err)
	assert.DeepEqual(t, pids2, pids)
	assert.DeepEqual(t, m2.Status()["keep"].SwarmKey, swarmKey)
	httpPort2, err := u2.GetPortHttp(u2.TNS().Node())
	assert.NilError(t, err)
	assert.Equal(t, httpPort2, httpPort, "a free saved port is reused")

	v, err := u2.TNS().KV().Get(t.Context(), "/kept")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "value")
}
