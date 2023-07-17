package tests

import (
	_ "embed"
	"testing"

	_ "bitbucket.org/taubyte/auth/service"
	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/odo/protocols/billing/api/p2p"
	_ "github.com/taubyte/odo/protocols/billing/service"
)

func TestClientWithUniverse(t *testing.T) {
	u := dreamland.Multiverse("single")
	defer u.Stop()

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":    {},
			"billing": {},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
					Billing: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	node := simple.Billing()
	if node == nil {
		t.Error("Billing node is nil")
		return
	}
}
