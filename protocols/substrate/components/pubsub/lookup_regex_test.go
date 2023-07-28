package pubsub

import (
	"context"
	"testing"

	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
	"github.com/taubyte/tau/protocols/substrate/components/structure"
)

func TestLookupRegex(t *testing.T) {
	s := NewTestService(peer.MockNode(context.Background()))

	msg := map[string]structureSpec.Messaging{
		"someMessagingId": {
			Name:      "Somemessaging",
			Match:     testChannel + "*",
			Regex:     true,
			WebSocket: true,
		},
	}

	function := map[string]structureSpec.Function{"someFuncId": {
		Name:    "someFunc",
		Channel: testChannel,
	}}

	structure.RefreshTestVariables()
	refreshTestVariables()
	fakeFetch(msg, function)
	_, err := s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel + "/zing",
			Project: testProject,
		})
	if err != nil {
		t.Error(err)
		return
	}
	if attachedTestWebSockets["Somemessaging"] != 1 {
		t.Errorf(`Got %#v expected {"Somemessaging":1}`, attachedTestWebSockets)
		return
	}

	fakeFetch(msg, function)
	_, err = s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel,
			Project: testProject,
		})
	if err != nil {
		t.Error(err)
		return
	}
	if attachedTestWebSockets["Somemessaging"] != 2 {
		t.Errorf(`Got %#v expected {"Somemessaging":2}`, attachedTestWebSockets)
		return
	}
}
