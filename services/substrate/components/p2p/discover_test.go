package p2p_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/services/substrate"
	"github.com/taubyte/tau/services/substrate/components/p2p"
	"gotest.tools/v3/assert"
)

func TestService_Discover(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})

	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
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
