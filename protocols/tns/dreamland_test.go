package tns

import (
	"reflect"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/libdream"
)

func TestDreamlandDoubleClient(t *testing.T) {
	u := libdream.Multiverse(libdream.UniverseConfig{Name: "single"})
	defer libdream.Zeno()

	err := u.StartWithConfig(&libdream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},

		Simples: map[string]libdream.SimpleConfig{
			"client": {
				Clients: libdream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
			"client2": {
				Clients: libdream.SimpleConfigClients{
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

	testKey := []string{"orange"}
	testValue := "someOrange"

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	simple2, err := u.Simple("client2")
	if err != nil {
		t.Error(err)
		return
	}

	tnsClient := simple.TNS()
	err = tnsClient.Push(testKey, testValue)
	if err != nil {
		t.Error(err)
		return
	}

	tnsClient2 := simple2.TNS()
	val, err := tnsClient2.Fetch(spec.NewTnsPath(testKey))
	if err != nil {
		t.Error(err)
		return
	}

	if reflect.DeepEqual(val.Interface(), testValue) == false {
		t.Errorf("Values not equal `%s` != `%s`", val, testValue)
		return
	}
}
