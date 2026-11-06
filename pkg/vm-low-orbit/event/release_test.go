package event

import (
	"net/http/httptest"
	"testing"
)

// A pooled instance serves many calls through one factory. Each call's event
// is released when the call returns, so a long-lived instance holds no request
// it is done with: before, every request an instance ever served stayed
// reachable from its factory.
func TestReleasedEventsAreForgotten(t *testing.T) {
	f := &Factory{events: make(map[uint32]*Event)}

	for i := 0; i < 1000; i++ {
		r := httptest.NewRequest("GET", "https://x.example/hello?k=v", nil)
		e := f.CreateHttpEvent(httptest.NewRecorder(), r)
		if _, err := f.getEvent(e.Id); err != 0 {
			t.Fatalf("event %d not registered during its call: %v", e.Id, err)
		}
		f.ReleaseEvent(e.Id)
	}

	f.eventsLock.RLock()
	defer f.eventsLock.RUnlock()
	if n := len(f.events); n != 0 {
		t.Fatalf("%d events still held after their calls returned", n)
	}
}
