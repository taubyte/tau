package pubsub

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/go-interfaces/services/tns"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/websocket"
	"github.com/taubyte/tau/protocols/substrate/components/structure"
	"github.com/taubyte/tau/vm/cache"
)

func init() {
	websocket.AttachWebSocket = func(f *websocket.WebSocket) error {
		v, ok := attachedTestWebSockets[f.Name()]
		if !ok {
			attachedTestWebSockets[f.Name()] = 1
		} else {
			attachedTestWebSockets[f.Name()] = v + 1
		}
		return nil
	}
}

var (
	testProject            = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	testChannel            = "someChannel"
	testCommit             = "qwertyuiop"
	attachedTestWebSockets = make(map[string]int)
)

func refreshTestVariables() {
	attachedTestWebSockets = make(map[string]int)
}

func fakeFetch(messagings map[string]structureSpec.Messaging, functions map[string]structureSpec.Function) {
	structure.FakeFetchMethod = func(path tns.Path) (tns.Object, error) {
		if path.String() == fmt.Sprintf("projects/%s/branches/master/current", testProject) {
			return structure.ResponseObject{Object: testCommit}, nil
		}

		if path.Slice()[6] == "messaging" {
			return structure.ResponseObject{Object: messagings}, nil
		} else if path.Slice()[6] == "functions" {
			return structure.ResponseObject{Object: functions}, nil
		}

		return nil, errors.New("Nothing found here")
	}
}

func NewTestService(node peer.Node) *Service {
	ctx := context.Background()

	s := &Service{
		Service: structure.MockNodeService(peer.MockNode(ctx), ctx),
		cache:   cache.New(),
	}

	return s
}
