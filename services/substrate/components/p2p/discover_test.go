//go:build dreaming

package p2p_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/services/substrate/components/p2p"
	_ "github.com/taubyte/tau/services/substrate/dream"
	"gotest.tools/v3/assert"
)

func TestService_Discover_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"substrate": {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]dream.SimpleConfig{},
	})
	assert.NilError(t, err)

	srv, err := p2p.New(u.Substrate())
	assert.NilError(t, err)

	peers, err := srv.Discover(u.Context(), 2, 2*time.Second)
	assert.NilError(t, err)

	assert.Equal(t, len(peers), 1)
}
