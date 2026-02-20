//go:build dreaming

package tns_test

import (
	"reflect"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

func TestFetch_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
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

	tns, err := simple.TNS()
	assert.NilError(t, err)

	push := func(id string) error {
		err = tns.Push([]string{id}, fixture[id])
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
		resp, err := tns.Fetch(spec.NewTnsPath([]string{_id}))
		if err != nil {
			t.Error(err)
			return err
		}

		if !reflect.DeepEqual(resp.Interface(), fixture[id]) {
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
