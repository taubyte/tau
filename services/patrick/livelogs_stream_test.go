package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeConn struct {
	lock     sync.Mutex
	text     strings.Builder
	pings    int
	closes   []string
	writeErr error
}

func (c *fakeConn) ReadMessage() (int, []byte, error) { select {} }
func (c *fakeConn) WriteJSON(any) error               { return nil }
func (c *fakeConn) Close() error                      { return nil }
func (c *fakeConn) EnableWriteCompression(bool)       {}
func (c *fakeConn) SetCloseHandler(func(int, string) error) {
}

func (c *fakeConn) WriteMessage(t int, data []byte) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.writeErr != nil {
		return c.writeErr
	}
	switch t {
	case websocket.TextMessage:
		c.text.Write(data)
	case websocket.PingMessage:
		c.pings++
	case websocket.CloseMessage:
		c.closes = append(c.closes, string(data))
	}
	return nil
}

func (c *fakeConn) String() string {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.text.String()
}

func newTestStream(t *testing.T) (*logStream, *fakeConn, *fakeJobs) {
	t.Helper()
	old := liveLogsHeartbeat
	liveLogsHeartbeat = 5 * time.Millisecond
	t.Cleanup(func() { liveLogsHeartbeat = old })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := &fakeJobs{keys: map[string][]byte{}}
	conn := &fakeConn{}
	srv := &PatrickService{ctx: ctx, db: db, liveLogs: &liveLogs{jobs: map[string]*liveLog{}}}
	return &logStream{srv: srv, jid: "j", conn: conn, ctx: ctx, cancel: cancel}, conn, db
}

type streamResult struct {
	reason string
	err    error
}

func runStream(s *logStream) chan streamResult {
	res := make(chan streamResult, 1)
	go func() {
		r, err := s.stream()
		res <- streamResult{r, err}
	}()
	return res
}

func waitStream(t *testing.T, res chan streamResult) streamResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end")
		return streamResult{}
	}
}

func TestLogStream_FollowsUntilDone(t *testing.T) {
	s, conn, db := newTestStream(t)
	db.put(t, "/jobs/j")
	res := runStream(s)

	time.Sleep(20 * time.Millisecond)
	s.srv.liveLogs.add(chunk(0, "hello ", false))
	s.srv.liveLogs.add(chunk(6, "world", false))
	s.srv.liveLogs.add(chunk(11, "", true))

	if r := waitStream(t, res); r.err != nil || r.reason != "job finished" {
		t.Fatalf("%+v", r)
	}
	if conn.String() != "hello world" {
		t.Fatalf("got %q", conn.String())
	}
	if s.srv.liveLogs.jobs["j"].watchers != 0 {
		t.Fatal("stream still counted as watching")
	}
}

func TestLogStream_UnknownJob(t *testing.T) {
	s, _, _ := newTestStream(t)
	if r := waitStream(t, runStream(s)); r.err == nil || !strings.Contains(r.err.Error(), "not found") {
		t.Fatalf("%+v", r)
	}
	if len(s.srv.liveLogs.jobs) != 0 {
		t.Fatal("an unknown job took a place in memory")
	}
}

func TestLogStream_LostFinalChunkEndsOnArchive(t *testing.T) {
	s, conn, db := newTestStream(t)
	s.srv.liveLogs.add(chunk(0, "start", false))
	res := runStream(s)

	time.Sleep(20 * time.Millisecond)
	select {
	case r := <-res:
		t.Fatalf("ended before the job did: %+v", r)
	default:
	}

	db.put(t, "/archive/jobs/j")
	if r := waitStream(t, res); r.err != nil || r.reason != "job finished" {
		t.Fatalf("%+v", r)
	}
	if got := conn.String(); !strings.HasPrefix(got, "start") || !strings.Contains(got, "not received") {
		t.Fatalf("got %q", got)
	}
}

func TestLogStream_PingsWhileQuiet(t *testing.T) {
	s, conn, _ := newTestStream(t)
	s.srv.liveLogs.add(chunk(0, "a", false))
	res := runStream(s)

	time.Sleep(40 * time.Millisecond)
	conn.lock.Lock()
	pings := conn.pings
	conn.writeErr = errors.New("client gone")
	conn.lock.Unlock()
	if pings == 0 {
		t.Fatal("no pings")
	}

	if r := waitStream(t, res); r.err == nil {
		t.Fatalf("a dead client did not end the stream: %+v", r)
	}
}

func TestLogStream_CloseReasonFitsAFrame(t *testing.T) {
	s, conn, _ := newTestStream(t)
	s.closeWith(websocket.CloseInternalServerErr, strings.Repeat("x", 500))
	if len(conn.closes) != 1 || len(conn.closes[0]) > 125 {
		t.Fatalf("close frames: %d, size %d", len(conn.closes), len(conn.closes[0]))
	}
}
