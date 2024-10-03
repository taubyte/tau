package event

import (
	"context"
	"net/http"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/errno"
	vmCommon "github.com/taubyte/tau/core/vm"
)

func (f *Factory) AttachEvent(e *Event) {
	f.eventsLock.Lock()
	defer f.eventsLock.Unlock()
	f.events[e.Id] = e
}

func (f *Factory) CreatePubsubEvent(msg *pubsub.Message) *Event {
	e := &Event{
		Id:     f.generateEventId(),
		Type:   common.EventTypePubsub,
		pubsub: msg,
	}
	f.eventsLock.Lock()
	defer f.eventsLock.Unlock()
	f.events[e.Id] = e
	return e
}

func (f *Factory) CreateHttpEvent(w http.ResponseWriter, r *http.Request) *Event {
	q := r.URL.Query()
	qKeys := make([]string, 0, len(q))
	for k := range q {
		qKeys = append(qKeys, k)
	}

	hKeys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		hKeys = append(hKeys, k)
	}

	e := &Event{
		Id:   f.generateEventId(),
		Type: common.EventTypeHttp,
		http: &httpEventAttributes{
			r:          r,
			w:          w,
			queryVars:  qKeys,
			headerVars: hKeys,
		},
	}

	f.eventsLock.Lock()
	defer f.eventsLock.Unlock()
	f.events[e.Id] = e
	return e
}

func (f *Factory) getEvent(eventID uint32) (*Event, errno.Error) {
	f.eventsLock.RLock()
	defer f.eventsLock.RUnlock()
	if e, exists := f.events[eventID]; exists {
		return e, 0
	}
	return nil, errno.ErrorEventNotFound
}

func (f *Factory) getEventWriter(eventID uint32) (http.ResponseWriter, errno.Error) {
	e, err := f.getEvent(eventID)
	if err != 0 {
		return nil, err
	}
	if e.http.w == nil {
		return nil, errno.ErrorNilAddress
	}

	return e.http.w, 0
}
func (f *Factory) getEventRequest(eventID uint32) (*http.Request, errno.Error) {
	e, err := f.getEvent(eventID)
	if err != 0 {
		return nil, err
	}
	if e.http.r == nil {
		return nil, errno.ErrorNilAddress
	}

	return e.http.r, 0
}

func (f *Factory) generateEventId() uint32 {
	f.eventsLock.Lock()
	defer func() {
		f.eventsIdToGrab += 1
		f.eventsLock.Unlock()
	}()
	return f.eventsIdToGrab
}

func (e *Event) TypeU64() uint32 {
	return uint32(e.Type)
}

func (f *Factory) W_getEventType(ctx context.Context, module vmCommon.Module, eventId uint32, typeIdPtr uint32) {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return
	}

	f.WriteUint32Le(module, typeIdPtr, e.TypeU64())
}
