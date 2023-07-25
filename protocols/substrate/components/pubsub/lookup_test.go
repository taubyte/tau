package pubsub

import (
	"reflect"
	"testing"

	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/substrate/components/pubsub/common"
	"github.com/taubyte/odo/protocols/substrate/components/structure"
)

func TestLookup(t *testing.T) {
	s := NewTestService(nil)
	msg := map[string]structureSpec.Messaging{
		"someMessagingId": {
			Name:  "Somemessaging",
			Match: testChannel,
		},
		"someMessagingId2": {
			Name:      "Somemessaging2",
			Match:     testChannel,
			WebSocket: true,
		},
		"someMessagingId3": {
			Name:      "Somemessaging3",
			Match:     testChannel,
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
	matcher := &common.MatchDefinition{
		Channel: testChannel,
		Project: testProject,
	}

	matches, err := s.Lookup(matcher)
	if err != nil {
		t.Error(err)
		return
	}

	if len(matches) != 2 {
		t.Errorf("Expected `2` matches got `%d`", len(matches))
		return
	}

	for _, serv := range matches {
		match, ok := serv.Matcher().(*common.MatchDefinition)
		if ok == false {
			t.Error("Serviceable matcher is not a pubsub match definition")
			return
		}

		if reflect.DeepEqual(match, matcher) == false {
			t.Error("Serviceable matcher is not equal to given matcher")
			return
		}
	}
}
