package event

import (
	"context"
	"net/http"
	"sync"

	"github.com/taubyte/go-sdk/common"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent         vm.Instance
	ctx            context.Context
	eventsLock     sync.RWMutex
	eventsIdToGrab uint32
	events         map[uint32]*Event
}

var _ vm.Factory = &Factory{}

type Event struct {
	Id     uint32
	Type   common.EventType
	http   *httpEventAttributes
	pubsub pubsubIface.Message
	p2p    *P2PData
}

type httpEventAttributes struct {
	r          *http.Request
	w          http.ResponseWriter
	queryVars  []string
	headerVars []string
}
