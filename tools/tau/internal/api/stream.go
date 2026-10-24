package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LogStream reads one run of a job's log, chunk by chunk, resuming at the byte
// offset it had reached when the connection drops. EOF from the frontend is the
// end of that run's log: the frontend holds the stream open while the run is to
// come and while it runs, and closes it when there is nothing more to come.
//
// THE RUN IS PINNED: the first response names it and every resume names it
// back. An offset is a position in one run's log, and a job whose node went
// away runs again with a log of its own, from byte zero — resumed without its
// run, the new log's bytes would carry on from the old one's offset as if they
// were the rest of it. A run whose log was not kept ends the stream with a
// *LostLog rather than EOF.
type LogStream struct {
	c       *Client
	ctx     context.Context
	job     string
	attempt int32
	offset  int64
	body    io.ReadCloser
	buf     []byte

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
	return &LogStream{c: c, ctx: ctx, job: job, attempt: attempt, offset: from, buf: make([]byte, 32<<10), Retries: 5, Backoff: 500 * time.Millisecond}
}

// Offset is how many bytes of the run's log have been read.
func (s *LogStream) Offset() int64 { return s.offset }

// Attempt is the run being read, or 0 before the frontend has named it.
func (s *LogStream) Attempt() int32 { return s.attempt }

// Next returns the next chunk, or io.EOF at the end of the log.
func (s *LogStream) Next() ([]byte, error) {
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
			s.attempt, s.body = run, body
		}
		n, err := s.body.Read(s.buf)
		if n > 0 {
			s.offset += int64(n)
			chunk := append([]byte(nil), s.buf[:n]...)
			if err != nil && !errors.Is(err, io.EOF) {
				s.drop()
			}
			return chunk, nil
		}
		if errors.Is(err, io.EOF) {
			s.Close()
			return nil, io.EOF
		}
		if err != nil {
			s.drop()
			if s.ctx.Err() != nil || failures >= s.Retries {
				return nil, err
			}
			failures++
			if !s.wait() {
				return nil, s.ctx.Err()
			}
		}
	}
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
		s.body = nil
	}
}

// Close releases the connection.
func (s *LogStream) Close() error {
	s.drop()
	return nil
}

// CopyTo writes the whole log to w, resuming across drops, and returns the
// final offset.
func (s *LogStream) CopyTo(w io.Writer) (int64, error) {
	defer s.Close()
	for {
		chunk, err := s.Next()
		if errors.Is(err, io.EOF) {
			return s.offset, nil
		}
		if err != nil {
			return s.offset, err
		}
		if _, err := w.Write(chunk); err != nil {
			return s.offset, err
		}
	}
}
