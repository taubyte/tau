package structure_test

import (
	"reflect"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/pkg/config-compiler/fixtures"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/services/tns"
	"gotest.tools/v3/assert"
)

func TestGetById(t *testing.T) {
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

func (s testStructure[T]) runGetIdTest() bool {
	test := func(g tns.StructureGetter[T]) bool {
		resourceMap, _, _, err := g.List()
		if err != nil {
			s.t.Error(err)
			return false
		}

		for id, resource := range resourceMap {
			_resource, err := g.GetById(id)
			if err != nil {
				s.t.Error(err)
				return false
			}

			if !reflect.DeepEqual(resource, _resource) {
				s.t.Errorf("(%T) Expected %v, got %v", new(T), resource, _resource)
				return false
			}
		}

		return true
	}

	allIface := s.iface.All(testProjectId, testAppId, testBranches...)
	if !test(allIface) {
		return false
	}

	globalIface := s.iface.Global(testProjectId, testBranches...)
	if !test(globalIface) {
		return false
	}

	relativeIface := s.iface.Relative(testProjectId, testAppId, testBranches...)
	return test(relativeIface)
}
