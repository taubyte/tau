package tns_test

import (
	"reflect"
	"testing"
	"time"

	p2p "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

var _ iface.Client = &p2p.Client{}

func TestTNSClient(t *testing.T) {
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

	fixture := map[string]interface{}{
		"/t1": false,
		"/t2": map[interface{}]interface{}{
			"a": uint64(1),
			"b": "string",
		},
	}

	tns, err := simple.TNS()
	assert.NilError(t, err)

	err = tns.Push([]string{"/t2"}, fixture["/t2"])
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(3 * time.Second)

	new_obj, err := tns.Fetch(spec.NewTnsPath([]string{"t2"}))
	if err != nil {
		t.Error(err)
		return
	}

	if !reflect.DeepEqual(fixture["/t2"], new_obj.Interface()) {
		t.Errorf(`Objects are not equal:
		Sent:            \n%#v
		Received:        \n%#v
		`, fixture["/t2"], new_obj)
		return
	}
	// Give time to clean up context.
	time.Sleep(10 * time.Second)

	keys, err := tns.List(1)
	if err != nil {
		t.Error(err)
		return

	}

	if len(keys) != 1 {
		t.Errorf("Expecing one unique key got %d", len(keys))
	}
}
