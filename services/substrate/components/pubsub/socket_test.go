package pubsub

import (
	"context"
	"fmt"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/structure"

	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	multihash "github.com/taubyte/tau/utils/multihash"
)

func TestSocketLookup(t *testing.T) {
	t.Skip("Websocket as serviseable needs to be refactored")
	testMessagingName := "someMessaging"
	s := NewTestService(peer.Mock(context.Background()))

	structure.RefreshTestVariables()
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
	// _, err = s.Lookup(
	// 	&common.MatchDefinition{
	// 		Channel: testChannel,
	// 		Project: testProject,
	// 	})
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// if len(structure.AttachedTestFunctions) != 0 {
	// 	t.Errorf("Expected no functions to be attached got `%d` attached", len(structure.AttachedTestFunctions))
	// 	return
	// }

	url, err := s.WebSocketURL(testProject, "", testChannel)
	if err != nil {
		t.Error(err)
		return
	}
	projectHash := multihash.Hash(testProject)
	webSocketFormat := "ws-%s/%s"
	expectedURL := fmt.Sprintf(webSocketFormat, projectHash, testChannel)
	if url != expectedURL {
		t.Errorf("Expected url `%s` got `%s`", expectedURL, url)
		return
	}
}
