package structure_test

import (
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

func TestGetByNameBasic(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	u.StartWithConfig(&dream.Config{
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

	err = u.RunFixture("fakeProject")
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
