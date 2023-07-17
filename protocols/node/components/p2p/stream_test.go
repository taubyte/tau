package p2p

import (
	"testing"

	"bitbucket.org/taubyte/p2p/streams/command"
	"bitbucket.org/taubyte/vm-test-examples/structure"
	"github.com/taubyte/go-interfaces/p2p/streams"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/p2p/common"
)

var (
	testProtocol = "/some/protocol"
	testCommand  = "someCommand"
)

func TestHandleForMatcher(t *testing.T) {
	s := NewTestService(nil)

	structure.RefreshTestVariables()
	fakeFetch(map[string]structureSpec.Service{
		testServiceId: {
			Name:     testService,
			Protocol: testProtocol,
		},
	}, map[string]structureSpec.Function{
		testFunctionId: {
			Name:     testFunction,
			Type:     "p2p",
			Command:  testCommand,
			Protocol: testProtocol,
		},
	})

	testMatcher := common.MatchDefinition{
		Project:  testProject,
		Protocol: testProtocol,
		Command:  testCommand,
	}
	testData := []byte("Hello, world!")
	cmd := command.New(testCommand, streams.Body{
		"matcher": testMatcher,
		"data":    testData,
	})

	_, err := s.Handle(cmd)
	if err != nil {
		t.Error(err)
		return
	}

	called := structure.CalledTestFunctionsP2P[0]
	gotData := called["data"].([]byte)
	if string(gotData) != string(testData) {
		t.Errorf("Got %s expected %s", string(gotData), string(testData))
		return
	}

	command := called["command"].(string)
	if command != testMatcher.Command {
		t.Errorf("Got %s expected %s", command, testMatcher.Command)
	}

	protocol := called["protocol"].(string)
	if protocol != testMatcher.Protocol {
		t.Errorf("Got %s expected %s", protocol, testMatcher.Protocol)
	}
}
