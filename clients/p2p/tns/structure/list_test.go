package structure_test

import (
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/pkg/config-compiler/fixtures"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/services/tns"
	"gotest.tools/v3/assert"
)

func TestList(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
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
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Domain]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Domain(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Function]{
		t:                t,
		expectedGlobal:   3,
		expectedRelative: 1,
		iface:            tns.Function(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Library]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Library(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Messaging]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Messaging(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Service]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Service(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.SmartOp]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.SmartOp(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Storage]{
		t:                t,
		expectedGlobal:   2,
		expectedRelative: 1,
		iface:            tns.Storage(),
	}).runListTest() {
		return
	}

	if !(testStructure[*structureSpec.Website]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Website(),
	}).runListTest() {
		return
	}
}

func (s testStructure[T]) runListTest() bool {
	all, _, _, err := s.iface.All(testProjectId, testAppId, testBranches...).List()
	if err != nil {
		s.t.Error(err)
		return false
	}
	if len(all) != s.expectedGlobal+s.expectedRelative {
		s.t.Errorf("(%T) Expected %d resources, got %d", new(T), s.expectedGlobal+s.expectedRelative, len(all))
		return false
	}

	global, _, _, err := s.iface.Global(testProjectId, testBranches...).List()
	if err != nil {
		s.t.Error(err)
		return false
	}
	if len(global) != s.expectedGlobal {
		s.t.Errorf("(%T) Expected %d global resources, got %d", new(T), s.expectedGlobal, len(all))
		return false
	}

	relative, _, _, err := s.iface.Relative(testProjectId, testAppId, testBranches...).List()
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
