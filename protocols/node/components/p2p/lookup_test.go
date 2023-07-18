package p2p

import (
	"reflect"
	"testing"

	iface "github.com/taubyte/go-interfaces/services/substrate/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/vm/lookup"
)

var testServices = map[string]structureSpec.Service{
	testServiceId: {
		Name:     testService,
		Protocol: testProtocol,
	},
}

var testFunctions = map[string]structureSpec.Function{
	testFunctionId: {
		Name:     testFunction,
		Type:     "p2p",
		Command:  "testCommand",
		Protocol: testProtocol,
	},
}

var testMatcher = &iface.MatchDefinition{
	Project:  testProject,
	Protocol: testProtocol,
	Command:  "testCommand",
}

func TestLookup(t *testing.T) {
	s := NewTestService(nil)
	fakeFetch(testServices, testFunctions)

	matches, err := lookup.Lookup(s, testMatcher)
	if err != nil {
		t.Error(err)
		return
	}

	if len(matches) != len(testFunctions) {
		t.Errorf("Expected `%d` matches, got `%d`", len(testFunctions), len(matches))
		return
	}

	matcher, ok := matches[0].Matcher().(*iface.MatchDefinition)
	if ok == false {
		t.Errorf("Received matcher is wrong type: got `%v` expected `%v`", matches[0].Matcher(), testMatcher)
		return
	}

	if reflect.DeepEqual(matcher, testMatcher) == false {
		t.Errorf("Expected received matcher and test matcher to be identical")
		return
	}

}
