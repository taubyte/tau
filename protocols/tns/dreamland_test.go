package tns

import (
	"reflect"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/services"
)

func TestDreamlandDoubleClient(t *testing.T) {
	u := services.Multiverse(services.UniverseConfig{Name: "single"})
	defer services.Zeno()

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
			"client2": {
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
