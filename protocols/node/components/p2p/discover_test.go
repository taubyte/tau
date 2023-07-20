package p2p_test

import (
	"testing"
	"time"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/odo/protocols/node/components/p2p"
	_ "github.com/taubyte/odo/protocols/node/service"
	"gotest.tools/assert"
)

func TestService_Discover(t *testing.T) {
	u := dreamland.Multiverse("TestService_Discover")

	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"node": {Others: map[string]int{"copies": 4}},
		},
		Simples: map[string]commonDreamland.SimpleConfig{},
	})
	assert.NilError(t, err)

	srv, err := p2p.New(u.Node())
	assert.NilError(t, err)

	peers, err := srv.Discover(u.Context(), 5, time.Second*2)
	assert.NilError(t, err)

	assert.Equal(t, len(peers), 3)
}
