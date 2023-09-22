package structure_test

import (
	"reflect"
	"testing"

	_ "github.com/taubyte/config-compiler/fixtures"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/tns"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/libdream/fixtures"
	_ "github.com/taubyte/tau/protocols/tns"
	"gotest.tools/v3/assert"
)

func TestGetById(t *testing.T) {
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

func (s testStructure[T]) runGetIdTest() bool {
	test := func(g tns.StructureGetter[T]) bool {
		resourceMap, err := g.List()
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

	allIface := s.iface.All(testProjectId, testAppId, testBranch)
	if !test(allIface) {
		return false
	}

	globalIface := s.iface.Global(testProjectId, testBranch)
	if !test(globalIface) {
		return false
	}

	relativeIface := s.iface.Relative(testProjectId, testAppId, testBranch)
	return test(relativeIface)
}
