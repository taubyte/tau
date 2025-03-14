package pubsub

import (
	"context"
	"fmt"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/structure"

	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	multihash "github.com/taubyte/utils/multihash"
)

func TestSocketLookup(t *testing.T) {
	testMessagingName := "someMessaging"
	s := NewTestService(peer.Mock(context.Background()))

	structure.RefreshTestVariables()
	refreshTestVariables()
	fakeFetch(nil, nil)
	_, err := s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel,
			Project: testProject,
		})
	if err == nil {
		t.Error("Expected lookup to fail with an error")
		return
	}

	msg := map[string]structureSpec.Messaging{"someMessagingId": {
		Name:      testMessagingName,
		Match:     testChannel,
		WebSocket: true,
	}}
	fakeFetch(msg, nil)
	_, err = s.Lookup(
		&common.MatchDefinition{
			Channel: testChannel,
			Project: testProject,
		})
	if err != nil {
		t.Error(err)
		return
	}

	if len(structure.AttachedTestFunctions) != 0 {
		t.Errorf("Expected no functions to be attached got `%d` attached", len(structure.AttachedTestFunctions))
		return
	}

	if len(attachedTestWebSockets) != 1 {
		t.Errorf("Expected 1 websocket to be attached got `%d` attached", len(attachedTestWebSockets))
		return
	}

	attachments, ok := attachedTestWebSockets[testMessagingName]
	if !ok {
		t.Errorf("messaging `%s` was not attached", testMessagingName)
		return
	}

	if attachments != 1 {
		t.Errorf("Expected messaging `%s` to be attached once got %d", testMessagingName, attachments)
	}

	url, err := s.WebSocketURL(testProject, "", testChannel)
	if err != nil {
		t.Error(err)
		return
	}
	projectHash := multihash.Hash(testProject)
	expectedURL := fmt.Sprintf(common.WebSocketFormat, projectHash, testChannel)
	if url != expectedURL {
		t.Errorf("Expected url `%s` got `%s`", expectedURL, url)
		return
	}
}
