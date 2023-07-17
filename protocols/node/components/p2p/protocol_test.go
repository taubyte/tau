package p2p

import (
	"context"
	"reflect"
	"testing"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	moodyCommon "bitbucket.org/taubyte/go-moody-blues/common"
	"bitbucket.org/taubyte/p2p/streams/client"
	"bitbucket.org/taubyte/vm-test-examples/structure"
	"github.com/taubyte/go-interfaces/p2p/streams"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/p2p/common"
)

// TODO: Needed?
func TestProtocolListen(t *testing.T) {
	moodyCommon.Dev = true
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

	u := dreamland.Multiverse("TestHandleForProject")
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

	receiverService := NewTestService(receiver.GetNode())
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

	sendData := streams.Body{
		"someData":      []byte("Hello, world"),
		"someotherData": "Hello from the other side",
	}

	p2pClient, err := client.New(context.Background(), sender.GetNode(), nil, protocolToUse, common.MinPeers, common.MaxPeers)
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
