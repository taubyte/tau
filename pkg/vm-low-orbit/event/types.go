package event

import (
	"bytes"
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

	// The guest may Write before it Returns its status; net/http would send
	// 200 on the first Write, so body and status are held until commit.
	status    int
	buf       bytes.Buffer
	committed bool
}

// maxBuffered caps the held body; past it the status so far is committed and
// the rest streams. shortcut: Return after >1 MiB of Write still loses the status.
const maxBuffered = 1 << 20

// commit sends the status (200 if unset) and the held body, once.
func (a *httpEventAttributes) commit() {
	if a.committed || a.w == nil {
		return
	}
	a.committed = true
	if a.status != 0 {
		a.w.WriteHeader(a.status)
	}
	if a.buf.Len() > 0 {
		a.w.Write(a.buf.Bytes())
		a.buf = bytes.Buffer{}
	}
}
