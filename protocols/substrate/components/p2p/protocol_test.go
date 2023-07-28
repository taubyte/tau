package p2p

import (
	"context"
	"reflect"
	"testing"

	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/p2p/streams/command"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	"github.com/taubyte/tau/protocols/substrate/components/p2p/common"
	"github.com/taubyte/tau/protocols/substrate/components/structure"
)

// TODO: Needed?
func TestProtocolListen(t *testing.T) {
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

	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: "TestHandleForProject"})
	err := u.StartWithConfig(&commonDreamland.Config{
		Simples: map[string]commonDreamland.SimpleConfig{
			"sender": {
				Clients: commonDreamland.SimpleConfigClients{},
			},
			"receiver": {
				Clients: commonDreamland.SimpleConfigClients{},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	receiver, err := u.Simple("receiver")
	if err != nil {
		t.Error(err)
		return
	}

	sender, err := u.Simple("sender")
	if err != nil {
		t.Error(err)
		return
	}

	receiverService := NewTestService(receiver.PeerNode())
	receiverService.stream, err = receiverService.StartStream(common.ServiceName, common.Protocol, receiverService.Handle)
	if err != nil {
		t.Error(err)
		return
	}

	stream, err := receiverService.Stream(context.Background(), testProject, "", testProtocol)
	if err != nil {
		t.Error(err)
		return
	}

	protocolToUse, err := stream.Listen()
	if err != nil {
		t.Error(err)
		return
	}

	sendData := command.Body{
		"someData":      []byte("Hello, world"),
		"someotherData": "Hello from the other side",
	}

	p2pClient, err := client.New(context.Background(), sender.PeerNode(), nil, protocolToUse, common.MinPeers, common.MaxPeers)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = p2pClient.Send(testCommand, sendData)
	if err != nil {
		t.Error(err)
		return
	}

	if reflect.DeepEqual(structure.CalledTestFunctionsP2P[0], sendData) == false {
		t.Errorf("Got: %#v\nexpected: %#v", structure.CalledTestFunctionsP2P[0], sendData)
		return
	}
}
