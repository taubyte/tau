package structure_test

import (
	"reflect"
	"testing"

	_ "github.com/taubyte/config-compiler/fixtures"
	"github.com/taubyte/go-interfaces/services/tns"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	_ "github.com/taubyte/tau/libdream/common/fixtures"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestGetById(t *testing.T) {
	u, tns, err := dreamland.BasicMultiverse("TestGetById").Tns()
	if err != nil {
		t.Error(err)
		return
	}
	defer u.Stop()

	err = u.RunFixture("fakeProject")
	if err != nil {
		t.Error(err)
		return
	}

	if (testStructure[*structureSpec.Database]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Database(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Domain]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Domain(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Function]{
		t:                t,
		expectedGlobal:   3,
		expectedRelative: 1,
		iface:            tns.Function(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Library]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Library(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Messaging]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Messaging(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Service]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Service(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.SmartOp]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.SmartOp(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Storage]{
		t:                t,
		expectedGlobal:   2,
		expectedRelative: 1,
		iface:            tns.Storage(),
	}).runGetIdTest() == false {
		return
	}

	if (testStructure[*structureSpec.Website]{
		t:                t,
		expectedGlobal:   1,
		expectedRelative: 1,
		iface:            tns.Website(),
	}).runGetIdTest() == false {
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

			if reflect.DeepEqual(resource, _resource) == false {
				s.t.Errorf("(%T) Expected %v, got %v", new(T), resource, _resource)
				return false
			}
		}

		return true
	}

	allIface := s.iface.All(testProjectId, testAppId, testBranch)
	if test(allIface) == false {
		return false
	}

	globalIface := s.iface.Global(testProjectId, testBranch)
	if test(globalIface) == false {
		return false
	}

	relativeIface := s.iface.Relative(testProjectId, testAppId, testBranch)
	return test(relativeIface)
}
