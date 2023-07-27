package tns_test

import (
	"reflect"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestFetch(t *testing.T) {
	u := dreamland.Multiverse("TestFetch")
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

	fixture := map[string]interface{}{
		"/t1": map[interface{}]interface{}{
			"a": uint64(6),
			"b": "otherstring",
		},
		"/t2": map[interface{}]interface{}{
			"a": uint64(1),
			"b": "string",
		},
		"/t22": map[interface{}]interface{}{
			"a": uint64(5),
			"b": "something different",
		},
	}

	push := func(id string) error {
		err = simple.TNS().Push([]string{id}, fixture[id])
		if err != nil {
			t.Error(err)
			return err
		}

		return nil
	}

	for key := range fixture {
		if push(key) != nil {
			return
		}
	}

	fetchAndCheck := func(id string) error {
		_id := id[1:]
		resp, err := simple.TNS().Fetch(spec.NewTnsPath([]string{_id}))
		if err != nil {
			t.Error(err)
			return err
		}

		if reflect.DeepEqual(resp.Interface(), fixture[id]) == false {
			t.Errorf("Objects not equal %v != %v", resp, fixture[id])
			return err
		}

		return nil
	}

	for key := range fixture {
		if fetchAndCheck(key) != nil {
			return
		}
	}
}
