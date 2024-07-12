package structure

import (
	"net/http"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
)

func init() {
	pterm.Info.Println("Initializing sdk with fake plugins")
	plugins.With = func(pi vm.PluginInstance) (plugins.Instance, error) {
		return &TestSdk{}, nil
	}
}

type TestSdk struct {
}

func (ts *TestSdk) CreateHttpEvent(w http.ResponseWriter, r *http.Request) *event.Event {
	CalledTestFunctionsHttp = append(CalledTestFunctionsHttp, httpEvent{W: w, R: r})
	return &event.Event{}
}

func (ts *TestSdk) CreatePubsubEvent(msg *pubsub.Message) *event.Event {
	CalledTestFunctionsPubsub = append(CalledTestFunctionsPubsub, msg)
	return &event.Event{}
}

func (ts *TestSdk) CreateP2PEvent(cmd *command.Command, response response.Response) *event.Event {
	CalledTestFunctionsP2P = append(CalledTestFunctionsP2P, cmd.Body)
	return &event.Event{}
}

func (ts *TestSdk) AttachEvent(*event.Event) {}
