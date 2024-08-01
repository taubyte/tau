package p2p

import (
	"context"
	"reflect"
	"testing"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/components/structure"
)

// TODO: Needed?
func TestProtocolListen(t *testing.T) {
	t.Skip("need to verify validity of this test")
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

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	err := u.StartWithConfig(&dream.Config{
		Simples: map[string]dream.SimpleConfig{
			"sender": {
				Clients: dream.SimpleConfigClients{}.Compat(),
			},
			"receiver": {
				Clients: dream.SimpleConfigClients{}.Compat(),
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
	receiverService.stream, err = receiverService.StartStream(common.SubstrateP2P, common.SubstrateP2P, receiverService.Handle)
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

	p2pClient, err := client.New(sender.PeerNode(), protocolToUse)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = p2pClient.Send(testCommand, sendData)
	if err != nil {
		t.Error(err)
		return
	}

	if !reflect.DeepEqual(structure.CalledTestFunctionsP2P[0], sendData) {
		t.Errorf("Got: %#v\nexpected: %#v", structure.CalledTestFunctionsP2P[0], sendData)
		return
	}
}
