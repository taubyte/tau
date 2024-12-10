package tns_test

import (
	"testing"
	"time"

	p2p "github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/clients/p2p/tns/common"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

func TestCache(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
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

	tns, err := simple.TNS()
	assert.NilError(t, err)

	_, err = tns.Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}

	_, err = tns.Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}

	// Pushing on a separate client so that it does not artificially update the cache
	{
		tnsClient, err := p2p.New(simple.PeerNode().Context(), simple.PeerNode())
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

	obj, err := tns.Fetch(spec.NewTnsPath([]string{"test"}))
	if err != nil {
		t.Error(err)
		return
	}
	if obj.Interface().(string) != "Hello, world" {
		t.Errorf("Expected object to be `Hello, world`  got %#v\n", obj)
		return
	}
}
