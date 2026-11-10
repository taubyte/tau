package api_test

import (
	"testing"
	"time"

	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"gotest.tools/v3/assert"
)

func TestApiVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, err := dream.New(t.Context(), dream.Name("apiversion"))
	assert.NilError(t, err)
	defer m.Close()
	assert.NilError(t, api.BigBang(m)) // free API port, never 1421

	c, err := client.New(t.Context(), client.URL("http://"+dream.DreamApiListen()), client.Timeout(10*time.Second))
	assert.NilError(t, err)
	v, err := c.Version()
	assert.NilError(t, err)
	assert.DeepEqual(t, v, dream.BuildVersion())
}
