package p2p_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/substrate"
	"github.com/taubyte/tau/protocols/substrate/components/p2p"
	"gotest.tools/assert"
)

func TestService_Discover(t *testing.T) {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})

	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"substrate": {Others: map[string]int{"copies": 4}},
		},
		Simples: map[string]dreamland.SimpleConfig{},
	})
	assert.NilError(t, err)

	srv, err := p2p.New(u.Substrate())
	assert.NilError(t, err)

	peers, err := srv.Discover(u.Context(), 5, time.Second*2)
	assert.NilError(t, err)

	assert.Equal(t, len(peers), 3)
}
