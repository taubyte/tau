package p2p

import (
	"context"
	"reflect"
	"testing"

	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/runtime/lookup"
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

var testMatcher = &p2p.MatchDefinition{
	Project:  testProject,
	Protocol: testProtocol,
	Command:  "testCommand",
}

func TestLookup(t *testing.T) {
	t.Skip("this test needs to be redone")
	s := NewTestService(peer.Mock(context.Background()))
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

	matcher, ok := matches[0].Matcher().(*p2p.MatchDefinition)
	if !ok {
		t.Errorf("Received matcher is wrong type: got `%v` expected `%v`", matches[0].Matcher(), testMatcher)
		return
	}

	if !reflect.DeepEqual(matcher, testMatcher) {
		t.Errorf("Expected received matcher and test matcher to be identical")
		return
	}

}
