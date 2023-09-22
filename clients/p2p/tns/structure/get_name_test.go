package structure_test

import (
	"testing"

	_ "github.com/taubyte/config-compiler/fixtures"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/tns"
	"gotest.tools/v3/assert"
)

func TestGetByNameBasic(t *testing.T) {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})

	err := u.RunFixture("fakeProject")
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	if !(testStructure[*structureSpec.Database]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Database(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Domain]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Domain(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Function]{
		t:                t,
		expectedGlobal:   3,
		expectedRelative: 1,
		iface:            tns.Function(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Library]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Library(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Messaging]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Messaging(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Service]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Service(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.SmartOp]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.SmartOp(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Storage]{
		t:                t,
		expectedGlobal:   2,
		expectedRelative: 1,
		iface:            tns.Storage(),
	}).runGetIdTest() {
		return
	}

	if !(testStructure[*structureSpec.Website]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Website(),
	}).runGetIdTest() {
		return
	}
}
