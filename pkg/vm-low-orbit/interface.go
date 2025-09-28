package taubyte

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/taubyte/tau/p2p/streams/command"
	res "github.com/taubyte/tau/p2p/streams/command/response"

	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
)

type Instance interface {
	eventApi
}

type eventApi interface {
	AttachEvent(*event.Event)

	CreateHttpEvent(w http.ResponseWriter, r *http.Request) *event.Event
	CreatePubsubEvent(msg pubsubIface.Message) *event.Event
	CreateP2PEvent(cmd *command.Command, response res.Response) *event.Event
}

var With = func(pi vm.PluginInstance) (Instance, error) {
	_pi, ok := pi.(*pluginInstance)
	if !ok {
		debug.PrintStack()
		return nil, fmt.Errorf("%v of type %T is not a Taubyte plugin instance", pi, pi)
	}

	if err := _pi.LoadAPIs(); err != nil {
		return nil, err
	}

	return _pi, nil
}

var _ eventApi = &event.Factory{}

func (i *pluginInstance) LoadAPIs() (err error) {
	if i.eventApi == nil {
		err = errors.New("eventApi not set ")
	}

	return
}
