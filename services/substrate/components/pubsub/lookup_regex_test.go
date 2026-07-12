package pubsub

import (
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/components/structure"
	"gotest.tools/v3/assert"
)

func TestLookupRegex(t *testing.T) {
	s := NewTestService(peer.Mock(t.Context()))
	msg := map[string]structureSpec.Messaging{
		"someMessagingId": {
			Name:      "Somemessaging",
			Match:     testChannel + ".*",
			Regex:     true,
			WebSocket: true,
		},
	}

	function := map[string]structureSpec.Function{"someFuncId": {
		Type:    "pubsub",
		Name:    "someFunc",
		Channel: testChannel,
	}}

	structure.RefreshTestVariables()
	fakeFetch(msg, function)

	// The messaging config matches on both the function and websocket path
	// (WebSocket: true), so a plain Lookup - one that doesn't request
	// WebSocket itself - picks up both: the function serviceable and the
	// otherwise-harmless websocket serviceable (its HandleMessage is a
	// no-op).
	ret, err := s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel + "/zing",
			Project: testProject,
		})
	assert.NilError(t, err)
	assert.Equal(t, len(ret), 2)
	assertOneFunctionOneWebSocket(t, ret)

	ret, err = s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel,
			Project: testProject,
		})
	assert.NilError(t, err)
	assert.Equal(t, len(ret), 2)
	assertOneFunctionOneWebSocket(t, ret)
}
