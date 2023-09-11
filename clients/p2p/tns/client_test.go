package tns_test

import (
	"reflect"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	iface "github.com/taubyte/go-interfaces/services/tns"
	spec "github.com/taubyte/go-specs/common"
	p2p "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/assert"
)

var _ iface.Client = &p2p.Client{}

func TestTNSClient(t *testing.T) {
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Conform(),
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
