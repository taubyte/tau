package tns_test

import (
	"testing"
	"time"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	p2p "github.com/taubyte/odo/clients/p2p/tns"
	"github.com/taubyte/odo/clients/p2p/tns/common"
)

func TestCache(t *testing.T) {
	u := dreamland.Multiverse("TestCache")
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}
	defer u.Stop()

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	common.ClientKeyCacheLifetime = 2 * time.Second

	_, err = simple.TNS().Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}

	_, err = simple.TNS().Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}

	// Pushing on a separate client so that it does not artificially update the cache
	{
		tnsClient, err := p2p.New(simple.GetNode().Context(), simple.GetNode())
		if err != nil {
			t.Error(err)
			return
		}
		err = tnsClient.Push([]string{"test"}, "Hello, world")
		if err != nil {
			t.Error(err)
			return
		}
	}

	obj, err := simple.TNS().Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}
	if obj.Interface().(string) != "Hello, world" {
		t.Errorf("Expected object to be `Hello, world`  got %#v\n", obj)
		return
	}
}
