package api

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// Event is one thing that happened to a run of a job: its status as it moves, or a
// line of its output.
type Event = buildv2.Job_Event

// LogStream reads one run of a job's events, one per line of JSON, resuming at the
// offset the last one carried after a drop; EOF is the end of that run's log. The
// run is pinned: a job that runs again has a new log from offset zero, so resuming
// without the run would splice two logs. A run whose log was not kept ends with
// *LostLog.
type LogStream struct {
	c       *Client
	ctx     context.Context
	job     string
	attempt int32
	offset  int64
	body    io.ReadCloser
	lines   *bufio.Reader
	ended   buildv2.JobStatus
	settled bool

	// Retries is how many consecutive reconnects a drop may cost before Next
	// gives up; Backoff is the wait before each.
	Retries int
	Backoff time.Duration
	// Snapshot ends the log at what it holds when it is opened rather than
	// when the job ends: what a running job has logged so far.
	Snapshot bool
}

// LostLog is a run whose log was not kept — its node went away before storing
// it, or the store would not take it — so what was read of it is all there is.
type LostLog struct {
	Job     string
	Attempt int32
	// Offset is how much of it was read.
	Offset int64
}

func (e *LostLog) Error() string {
	if e.Offset == 0 {
		return fmt.Sprintf("job %s: the log of attempt %d was not kept", e.Job, e.Attempt)
	}
	return fmt.Sprintf("job %s: the log of attempt %d was not kept past byte %d", e.Job, e.Attempt, e.Offset)
}

// Stream opens a resumable log stream for one run of job, starting at from:
// the run attempt names, or with 0 the job's current one.
func (c *Client) Stream(ctx context.Context, job string, attempt int32, from int64) *LogStream {
	return &LogStream{c: c, ctx: ctx, job: job, attempt: attempt, offset: from, Retries: 5, Backoff: 500 * time.Millisecond}
}

// Offset is where in the run's log the last event read ended.
func (s *LogStream) Offset() int64 { return s.offset }

// Attempt is the run being read, or 0 before the build service has named it.
func (s *LogStream) Attempt() int32 { return s.attempt }

// Ended is the status the run's log ended the job in, once it has said one.
func (s *LogStream) Ended() (buildv2.JobStatus, bool) { return s.ended, s.settled }

// eventJSON reads what the build service writes, and what it may add.
var eventJSON = protojson.UnmarshalOptions{DiscardUnknown: true}

// Next returns the next event, or io.EOF at the end of the log.
func (s *LogStream) Next() (*Event, error) {
	failures := 0
	for {
		if s.body == nil {
			body, run, err := s.c.logs(s.ctx, s.job, s.attempt, s.offset, s.Snapshot)
			if err != nil {
				var refusal *Error
				if errors.As(err, &refusal) {
					if refusal.Status == http.StatusGone {
						return nil, s.lost(refusal.Header)
					}
					return nil, err
				}
				if errors.Is(err, errUnnamedRun) {
					return nil, err
				}
				if s.ctx.Err() != nil || failures >= s.Retries {
					// %v, not %w: a transport that failed with io.EOF has not
					// reached the end of the log, and must not read as if it had.
					return nil, fmt.Errorf("reading the log of %s: %v", s.job, err)
				}
				failures++
				if !s.wait() {
					return nil, s.ctx.Err()
				}
				continue
			}
			if s.attempt != 0 && run != s.attempt {
				body.Close()
				return nil, fmt.Errorf("reading the log of %s: attempt %d was asked for and attempt %d was sent", s.job, s.attempt, run)
			}
			s.attempt, s.body, s.lines = run, body, bufio.NewReader(body)
		}
		line, err := s.lines.ReadBytes('\n')
		if bytes.HasSuffix(line, []byte("\n")) {
			ev := &Event{}
			if err := eventJSON.Unmarshal(line, ev); err != nil {
				s.drop()
				return nil, fmt.Errorf("reading the log of %s: %w", s.job, err)
			}
			s.offset = ev.GetOffset()
			if st, ok := ev.GetKind().(*buildv2.Job_Event_Status); ok {
				s.ended, s.settled = st.Status, isEnd(st.Status)
			}
			return ev, nil
		}
		// An event cut in the middle is not one: it is read again from the offset
		// the last whole one carried.
		if errors.Is(err, io.EOF) && len(line) == 0 {
			s.Close()
			return nil, io.EOF
		}
		s.drop()
		if s.ctx.Err() != nil || failures >= s.Retries {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		failures++
		if !s.wait() {
			return nil, s.ctx.Err()
		}
	}
}

// Line returns the next line of the run's output, with its newline, passing over
// its statuses: what a viewer of the log reads.
func (s *LogStream) Line() ([]byte, error) {
	for {
		ev, err := s.Next()
		if err != nil {
			return nil, err
		}
		if m, ok := ev.GetKind().(*buildv2.Job_Event_Message); ok {
			return []byte(m.Message + "\n"), nil
		}
	}
}

func isEnd(st buildv2.JobStatus) bool {
	switch st {
	case buildv2.JobStatus_JobStatusSuccess, buildv2.JobStatus_JobStatusFailed, buildv2.JobStatus_JobStatusCancelled:
		return true
	}
	return false
}

// lost is the run a 410 is about: the one pinned, or the one it names.
func (s *LogStream) lost(h http.Header) *LostLog {
	run := s.attempt
	if run == 0 {
		run = runOf(h)
	}
	return &LostLog{Job: s.job, Attempt: run, Offset: s.offset}
}

func (s *LogStream) wait() bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(s.Backoff):
		return true
	}
}

func (s *LogStream) drop() {
	if s.body != nil {
		s.body.Close()
		s.body, s.lines = nil, nil
	}
}

func (s *LogStream) Close() error {
	s.drop()
	return nil
}

// CopyTo writes the run's output to w, a line per message, resuming across drops,
// and returns the final offset.
func (s *LogStream) CopyTo(w io.Writer) (int64, error) {
	return s.Each(func(ev *Event) error {
		if m, ok := ev.GetKind().(*buildv2.Job_Event_Message); ok {
			_, err := io.WriteString(w, m.Message+"\n")
			return err
		}
		return nil
	})
}

// Each hands every event of the run to fn, resuming across drops, and returns the
// final offset.
func (s *LogStream) Each(fn func(*Event) error) (int64, error) {
	defer s.Close()
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			return s.offset, nil
		}
		if err != nil {
			return s.offset, err
		}
		if err := fn(ev); err != nil {
			return s.offset, err
		}
	}
}
