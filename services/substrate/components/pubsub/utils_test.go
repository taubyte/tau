package pubsub

import (
	"context"
	"errors"
	"fmt"
	"testing"

	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/structure"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

var (
	testProject = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	testChannel = "someChannel"
	testCommit  = "qwertyuiop"
)

func fakeFetch(messagings map[string]structureSpec.Messaging, functions map[string]structureSpec.Function) {
	structure.FakeFetchMethod = func(path tns.Path) (tns.Object, error) {
		if path.String() == fmt.Sprintf("projects/%s/branches/master/current", testProject) {
			return structure.ResponseObject{Object: testCommit}, nil
		}

		p := path.Slice()
		if len(p) >= 6 && p[6] == "messaging" {
			return structure.ResponseObject{Object: messagings}, nil
		} else if len(p) >= 6 && p[6] == "functions" {
			return structure.ResponseObject{Object: functions}, nil
		}

		return nil, errors.New("Nothing found here")
	}
}

func NewTestService(node peer.Node) *Service {
	ctx := context.Background()

	s := &Service{
		Service: structure.MockNodeService(peer.Mock(ctx), ctx),
		cache:   cache.New(),
	}

	return s
}

// assertOneFunctionOneWebSocket checks a Lookup result holds exactly one
// function serviceable (Config() != nil) and one websocket serviceable
// (Config() == nil, per the websocket package's Config()).
func assertOneFunctionOneWebSocket(t *testing.T, picks []iface.Serviceable) {
	t.Helper()

	var functions, webSockets int
	for _, p := range picks {
		if p.Config() != nil {
			functions++
		} else {
			webSockets++
		}
	}

	if functions != 1 || webSockets != 1 {
		t.Errorf("expected 1 function and 1 websocket serviceable, got %d function(s) and %d websocket(s)", functions, webSockets)
	}
}
