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

func TestList(t *testing.T) {
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
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

	if (testStructure[*structureSpec.Database]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Database(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Domain]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Domain(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Function]{
		t:                t,
		expectedGlobal:   3,
		expectedRelative: 1,
		iface:            tns.Function(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Library]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Library(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Messaging]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Messaging(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Service]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Service(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.SmartOp]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.SmartOp(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Storage]{
		t:                t,
		expectedGlobal:   2,
		expectedRelative: 1,
		iface:            tns.Storage(),
	}).runListTest() == false {
		return
	}

	if (testStructure[*structureSpec.Website]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Website(),
	}).runListTest() == false {
		return
	}
}

func (s testStructure[T]) runListTest() bool {
	all, err := s.iface.All(testProjectId, testAppId, testBranch).List()
	if err != nil {
		s.t.Error(err)
		return false
	}
	if len(all) != s.expectedGlobal+s.expectedRelative {
		s.t.Errorf("(%T) Expected %d resources, got %d", new(T), s.expectedGlobal+s.expectedRelative, len(all))
		return false
	}

	global, err := s.iface.Global(testProjectId, testBranch).List()
	if err != nil {
		s.t.Error(err)
		return false
	}
	if len(global) != s.expectedGlobal {
		s.t.Errorf("(%T) Expected %d global resources, got %d", new(T), s.expectedGlobal, len(all))
		return false
	}

	relative, err := s.iface.Relative(testProjectId, testAppId, testBranch).List()
	if err != nil {
		s.t.Error(err)
		return false
	}
	if len(relative) != s.expectedRelative {
		s.t.Errorf("(%T) Expected %d relative resources, got %d", new(T), s.expectedRelative, len(all))
		return false
	}

	return true
}
