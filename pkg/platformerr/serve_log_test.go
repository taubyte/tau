package platformerr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type countLog struct{ debug, errors int }

func (l *countLog) Debugf(string, ...any) { l.debug++ }
func (l *countLog) Errorf(string, ...any) { l.errors++ }

func serveErr(l *countLog, err error) {
	Serve(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), l, func(http.ResponseWriter, *http.Request) error { return err })
}

// A refused request is normal back-pressure: under load one ERROR each is thousands of lines a minute.
func TestServeLogsRefusalsAtDebug(t *testing.T) {
	l := &countLog{}
	serveErr(l, New(NodeBusy, errors.New("full")))
	if l.errors != 0 || l.debug != 1 {
		t.Fatalf("node_busy: %d errors, %d debug", l.errors, l.debug)
	}
	l = &countLog{}
	serveErr(l, errors.New("boom"))
	if l.errors != 1 {
		t.Fatalf("a real failure must stay at error: %d", l.errors)
	}
}
