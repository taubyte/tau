package pubsub

import (
	"context"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/components/structure"
	"gotest.tools/v3/assert"
)

func TestLookupRegex(t *testing.T) {
	s := NewTestService(peer.Mock(context.Background()))
	msg := map[string]structureSpec.Messaging{
		"someMessagingId": {
			Name:      "Somemessaging",
			Match:     testChannel + ".*",
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

	ret, err := s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel + "/zing",
			Project: testProject,
		})
	assert.NilError(t, err)
	assert.Assert(t, len(ret) > 0)

	assert.Equal(t, attachedTestWebSockets["Somemessaging"], 1)

	ret, err = s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel,
			Project: testProject,
		})
	assert.NilError(t, err)
	assert.Assert(t, len(ret) > 0)

	assert.Equal(t, attachedTestWebSockets["Somemessaging"], 1)
}
