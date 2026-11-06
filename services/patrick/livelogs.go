package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	http "github.com/taubyte/tau/pkg/http"
	servicesCommon "github.com/taubyte/tau/services/common"
)

var (
	liveLogsMaxSize      = 16 * 1024 * 1024
	liveLogsMaxTotal     = 256 * 1024 * 1024 // all jobs together
	liveLogsMaxJobs      = 1024
	liveLogsMaxChunk     = 128 * 1024 // the monkey sends at most 64KiB
	liveLogsMaxPending   = 32
	liveLogsMaxRuns      = 16
	liveLogsKeepDone     = 10 * time.Minute
	liveLogsKeepIdle     = time.Hour
	liveLogsHeartbeat    = 30 * time.Second
	liveLogsWriteTimeout = 30 * time.Second
	liveLogsDoneGrace    = 2 * time.Second // pubsub can deliver the end before the last chunks
)

const (
	liveLogsMaxCloseReason = 120 // a WebSocket close frame holds 123 bytes
	liveLogsReadLimit      = 4096
)

// liveLog is a job's log as far as the monkey has published it.
type liveLog struct {
	run      string          // the run being followed
	oldRun   map[string]bool // runs replaced by a later one
	ending   bool            // the final chunk arrived; final is the log's length
	final    int64
	data     []byte
	next     int64            // monkey offset the next chunk should start at
	pending  map[int64][]byte // chunks that arrived ahead of next
	done     bool
	cut      bool // part of the log was dropped for being over a size limit
	watchers int  // streams following this log; it is kept while there are any
	updated  time.Time
	changed  chan struct{} // closed and replaced on every change
}

type liveLogs struct {
	lock  sync.Mutex
	jobs  map[string]*liveLog
	total int // bytes held by all of jobs
}

func (ll *liveLogs) get(jid string, create bool) *liveLog {
	l, ok := ll.jobs[jid]
	if !ok && create {
		if len(ll.jobs) >= liveLogsMaxJobs {
			return nil
		}
		l = &liveLog{pending: make(map[int64][]byte), updated: time.Now(), changed: make(chan struct{})}
		ll.jobs[jid] = l
	}
	return l
}

func (ll *liveLogs) add(chunk *servicesCommon.LiveLogChunk) {
	// the topic is open to the network's peers, so a chunk's size is not trusted
	if len(chunk.Data) > liveLogsMaxChunk {
		return
	}

	ll.lock.Lock()
	defer ll.lock.Unlock()

	l := ll.get(chunk.Job, true)
	if l == nil {
		return
	}
	if l.done {
		// a retried job keeps its id and writes a new log from the start;
		// streams still on the old log hold on to their own copy
		if chunk.Offset != 0 || len(chunk.Data) == 0 {
			return
		}
		ll.total -= len(l.data)
		delete(ll.jobs, chunk.Job)
		l = ll.get(chunk.Job, true)
	}

	if !ll.followRun(l, chunk) {
		return
	}

	if len(chunk.Data) > 0 && chunk.Offset >= l.next {
		l.pending[chunk.Offset] = chunk.Data
	}

	// pubsub does not keep order; take chunks once they are contiguous, and
	// skip a gap (Patrick started mid-job, or a chunk was lost) once too many
	// chunks wait behind it
	for {
		if data, ok := l.pending[l.next]; ok {
			delete(l.pending, l.next)
			ll.append(l, data)
			l.next += int64(len(data))
			continue
		}
		if len(l.pending) <= liveLogsMaxPending {
			break
		}
		first := int64(-1)
		for off := range l.pending {
			if first < 0 || off < first {
				first = off
			}
		}
		ll.append(l, []byte("\n[... part of this log was not received ...]\n"))
		l.next = first
	}

	if chunk.Done && !l.ending {
		l.ending, l.final = true, chunk.Offset
		run := l.run
		time.AfterFunc(liveLogsDoneGrace, func() {
			ll.lock.Lock()
			current := l.run == run
			ll.lock.Unlock()
			if current {
				ll.finish(l)
			}
		})
	}
	if l.ending && l.next >= l.final {
		l.done = true
	}

	l.updated = time.Now()
	close(l.changed)
	l.changed = make(chan struct{})
}

// followRun reports whether the chunk belongs to the run being followed, moving
// on to a new run when one starts: its offsets start over, and streams keep
// reading the same log, so they see the new run after a note.
func (ll *liveLogs) followRun(l *liveLog, chunk *servicesCommon.LiveLogChunk) bool {
	switch {
	case chunk.Run == l.run:
		return true
	case l.oldRun[chunk.Run]:
		return false
	case l.run == "":
		l.run = chunk.Run
		return true
	}

	if l.oldRun == nil {
		l.oldRun = make(map[string]bool)
	}
	if len(l.oldRun) >= liveLogsMaxRuns {
		return false
	}
	l.oldRun[l.run] = true
	l.run = chunk.Run
	l.pending = make(map[int64][]byte)
	l.next, l.ending, l.final = 0, false, 0
	ll.append(l, []byte("\n[... this job started again on another node ...]\n"))
	return true
}

// finish ends a log whose missing chunks did not arrive in time.
func (ll *liveLogs) finish(l *liveLog) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	if l.done {
		return
	}

	for len(l.pending) > 0 {
		first := int64(-1)
		for off := range l.pending {
			if first < 0 || off < first {
				first = off
			}
		}
		if first != l.next {
			ll.append(l, []byte("\n[... part of this log was not received ...]\n"))
		}
		data := l.pending[first]
		delete(l.pending, first)
		ll.append(l, data)
		l.next = first + int64(len(data))
	}
	if l.final > l.next {
		ll.append(l, []byte("\n[... the end of this log was not received ...]\n"))
	}
	l.done = true
	l.updated = time.Now()
	close(l.changed)
	l.changed = make(chan struct{})
}

func (ll *liveLogs) append(l *liveLog, data []byte) {
	room := min(liveLogsMaxSize-len(l.data), liveLogsMaxTotal-ll.total)
	if room >= len(data) {
		l.data = append(l.data, data...)
		ll.total += len(data)
		return
	}
	if !l.cut {
		l.cut = true
		note := "\n[... over the live log limit; the full log is available once the job is done ...]\n"
		l.data = append(l.data, note...)
		ll.total += len(note)
	}
}

// watch returns the job's log for a stream to follow, or nil if there is none
// and create is false or the limit on jobs is reached.
func (ll *liveLogs) watch(jid string, create bool) *liveLog {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	l := ll.get(jid, create)
	if l != nil {
		l.watchers++
	}
	return l
}

func (ll *liveLogs) unwatch(l *liveLog) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	l.watchers--
}

// reset forgets a job's log, for a job about to run again. A stream already on
// it keeps its own copy and ends with it.
func (ll *liveLogs) reset(jid string) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	if l, ok := ll.jobs[jid]; ok {
		ll.total -= len(l.data)
		delete(ll.jobs, jid)
	}
}

func (ll *liveLogs) gc() {
	ll.lock.Lock()
	defer ll.lock.Unlock()

	now := time.Now()
	for jid, l := range ll.jobs {
		if l.watchers > 0 {
			continue
		}
		if (l.done && now.Sub(l.updated) > liveLogsKeepDone) || now.Sub(l.updated) > liveLogsKeepIdle {
			ll.total -= len(l.data)
			delete(ll.jobs, jid)
		}
	}
}

func (srv *PatrickService) startLiveLogs(ctx context.Context) error {
	srv.liveLogs = &liveLogs{jobs: make(map[string]*liveLog)}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				srv.liveLogs.gc()
			}
		}
	}()

	return srv.subscribeLiveLogs(ctx)
}

func (srv *PatrickService) subscribeLiveLogs(ctx context.Context) error {
	return srv.node.PubSubSubscribe(
		servicesCommon.LiveLogsTopic,
		func(msg *pubsub.Message) {
			chunk := new(servicesCommon.LiveLogChunk)
			if cbor.Unmarshal(msg.Data, chunk) != nil || chunk.Job == "" {
				return
			}
			if !srv.liveLogSenders.allowed(ctx, srv.db, chunk.Job, msg.GetFrom()) {
				return
			}
			srv.liveLogs.add(chunk)
		},
		func(err error) {
			if ctx.Err() != nil {
				return
			}
			logger.Error("live logs subscription ended with:", err.Error())
			time.AfterFunc(time.Second, func() {
				if err := srv.subscribeLiveLogs(ctx); err != nil {
					logger.Error("resubscribing to live logs failed with:", err.Error())
				}
			})
		},
	)
}

// logStream sends one job's log over a WebSocket: what was published so far,
// then each new piece, and closes once the job is done. A job that finished
// before its log could be followed gets its stored log instead.
type logStream struct {
	srv    *PatrickService
	jid    string
	conn   http.WebSocketConnection
	ctx    context.Context
	cancel context.CancelFunc
}

func (srv *PatrickService) newLogStream(ctx http.Context, conn http.WebSocketConnection) http.WebSocketHandler {
	jid, err := ctx.GetStringVariable("jid")
	if err != nil {
		conn.Close()
		return nil
	}

	// the upgrade already happened, so a refusal is a close frame. A job not
	// known here yet is refused too: its live log may already be arriving from
	// its monkey, and the stream is only checked once, when it opens.
	if _, err = srv.anyJob(ctx.Request().Context(), jid); err != nil {
		err = fmt.Errorf("job `%s` not found", jid)
	} else {
		err = srv.authorizeJob(ctx, jid)
	}
	if err != nil {
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()))
		conn.Close()
		return nil
	}

	// the client only ever sends control frames
	if c, ok := conn.(interface{ SetReadLimit(int64) }); ok {
		c.SetReadLimit(liveLogsReadLimit)
	}

	// the request's context ends when the upgrade handler returns
	sctx, cancel := context.WithCancel(srv.ctx)
	return &logStream{srv: srv, jid: jid, conn: conn, ctx: sctx, cancel: cancel}
}

// In only reads, to notice the client going away.
func (s *logStream) In() {
	defer s.cancel()
	for {
		if _, _, err := s.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *logStream) Out() {
	defer s.conn.Close()
	defer s.cancel()

	reason, err := s.stream()
	if err != nil {
		s.closeWith(websocket.CloseInternalServerErr, err.Error())
		return
	}
	s.closeWith(websocket.CloseNormalClosure, reason)
}

func (s *logStream) closeWith(code int, reason string) {
	if len(reason) > liveLogsMaxCloseReason {
		reason = reason[:liveLogsMaxCloseReason]
	}
	s.write(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

// write gives up on a client that stopped reading, which would otherwise hold
// this stream until TCP gives up.
func (s *logStream) write(messageType int, data []byte) error {
	if c, ok := s.conn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		c.SetWriteDeadline(time.Now().Add(liveLogsWriteTimeout))
	}
	return s.conn.WriteMessage(messageType, data)
}

func (s *logStream) stream() (string, error) {
	ll := s.srv.liveLogs

	l := ll.watch(s.jid, false)
	if l == nil {
		if job, err := s.srv.getJob(s.ctx, "/archive/jobs/", s.jid); err == nil {
			return "job finished", s.sendStored(job.Logs)
		}
		if _, err := s.srv.getJob(s.ctx, "/jobs/", s.jid); err != nil {
			return "", fmt.Errorf("job `%s` not found", s.jid)
		}
		// queued or running: wait for its log to start
		if l = ll.watch(s.jid, true); l == nil {
			return "", errors.New("too many logs being followed, try again later")
		}
	}
	defer ll.unwatch(l)

	heartbeat := time.NewTicker(liveLogsHeartbeat)
	defer heartbeat.Stop()

	sent, archived := 0, 0
	for {
		ll.lock.Lock()
		data, done, changed := l.data[sent:], l.done, l.changed
		ll.lock.Unlock()

		if len(data) > 0 {
			if err := s.write(websocket.TextMessage, data); err != nil {
				return "", err
			}
			sent += len(data)
			continue
		}
		if done {
			return "job finished", nil
		}

		select {
		case <-s.ctx.Done():
			return "", s.ctx.Err()
		case <-changed:
		case <-heartbeat.C:
			if err := s.write(websocket.PingMessage, nil); err != nil {
				return "", err
			}
			// the final chunk can be lost, or the monkey can die: the job's
			// record decides. The chunk goes out before the job is archived,
			// so give it one more beat to arrive.
			if _, err := s.srv.getJob(s.ctx, "/archive/jobs/", s.jid); err != nil {
				archived = 0
			} else if archived++; archived > 1 {
				note := "\n[... the end of this log was not received; query the job's stored log ...]\n"
				if err := s.write(websocket.TextMessage, []byte(note)); err != nil {
					return "", err
				}
				return "job finished", nil
			}
		}
	}
}

func (s *logStream) sendStored(logs map[string]string) error {
	cids := make([]string, 0, len(logs))
	if cid, ok := logs[s.jid]; ok {
		cids = append(cids, cid)
	} else {
		for _, cid := range logs {
			cids = append(cids, cid)
		}
	}

	for _, cid := range cids {
		f, err := s.srv.node.GetFile(s.ctx, cid)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return err
		}
		if err = s.write(websocket.TextMessage, data); err != nil {
			return err
		}
	}
	return nil
}
