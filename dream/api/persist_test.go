package api_test

import (
	"os"
	"strings"
	"testing"
	"time"

	client "github.com/taubyte/tau/clients/http/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

// Universes made through the API persist by default; asking again resumes the
// running one; fresh deletes its root and starts it empty.
func TestApiUniversePersistsAndFreshEmpties(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m, err := dream.New(t.Context(), dream.Name("apipersist"))
	assert.NilError(t, err)
	defer m.Close()
	assert.NilError(t, api.BigBang(m)) // free API port, never 1421

	c, err := client.New(t.Context(), client.URL("http://"+dream.DreamApiListen()), client.Timeout(60*time.Second))
	assert.NilError(t, err)

	config := &dream.Config{Services: map[string]commonIface.ServiceConfig{"tns": {}}}

	assert.NilError(t, c.StartUniverseWithConfig("x", config))
	u, err := m.Universe("x")
	assert.NilError(t, err)
	assert.Assert(t, u.Persistent(), "API universe is not persistent")
	root := m.Status()["x"].Root
	assert.Assert(t, strings.HasPrefix(root, home), root)
	_, err = os.Stat(root + "/universe.json")
	assert.NilError(t, err)
	assert.NilError(t, u.TNS().KV().Put(t.Context(), "/kept", []byte("value")))

	// asking for it again resumes it, keeping the data
	assert.NilError(t, c.StartUniverseWithConfig("x", config))
	u, err = m.Universe("x")
	assert.NilError(t, err)
	v, err := u.TNS().KV().Get(t.Context(), "/kept")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "value")

	// fresh: empty, but still kept at the same root
	assert.NilError(t, c.StartFreshUniverseWithConfig("x", config))
	u, err = m.Universe("x")
	assert.NilError(t, err)
	assert.Assert(t, u.Running())
	assert.Assert(t, u.Persistent())
	assert.Equal(t, m.Status()["x"].Root, root)
	_, err = os.Stat(root + "/universe.json")
	assert.NilError(t, err)
	_, err = u.TNS().KV().Get(t.Context(), "/kept")
	assert.Assert(t, err != nil, "fresh universe still has the old data")
	assert.NilError(t, u.TNS().KV().Put(t.Context(), "/after", []byte("fresh")))

	// and it comes back after a restart
	assert.NilError(t, m.Close())
	m2, err := dream.New(t.Context(), dream.LoadPersistent(), dream.Name("apipersist"))
	assert.NilError(t, err)
	defer m2.Close()
	u2, err := m2.Universe("x")
	assert.NilError(t, err)
	assert.Assert(t, u2.Running())
	v, err = u2.TNS().KV().Get(t.Context(), "/after")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "fresh")
	_, err = u2.TNS().KV().Get(t.Context(), "/kept")
	assert.Assert(t, err != nil, "old data came back after fresh")
}
