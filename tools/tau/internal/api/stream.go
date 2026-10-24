package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// LogStream reads a job's log, chunk by chunk, resuming at the byte offset it
// had reached when the connection drops. EOF from the frontend is the end of
// the log: the frontend holds the stream open while the job is queued and
// while it runs, and closes it when there is nothing more to come.
type LogStream struct {
	c      *Client
	ctx    context.Context
	job    string
	offset int64
	body   io.ReadCloser
	buf    []byte

	// Retries is how many consecutive reconnects a drop may cost before Next
	// gives up; Backoff is the wait before each.
	Retries int
	Backoff time.Duration
	// Snapshot ends the log at what it holds when it is opened rather than
	// when the job ends: what a running job has logged so far.
	Snapshot bool
}

// Stream opens a resumable log stream for job, starting at from.
func (c *Client) Stream(ctx context.Context, job string, from int64) *LogStream {
	return &LogStream{c: c, ctx: ctx, job: job, offset: from, buf: make([]byte, 32<<10), Retries: 5, Backoff: 500 * time.Millisecond}
}

// Offset is how many bytes of the log have been read.
func (s *LogStream) Offset() int64 { return s.offset }

// Next returns the next chunk, or io.EOF at the end of the log.
func (s *LogStream) Next() ([]byte, error) {
	failures := 0
	for {
		if s.body == nil {
			body, err := s.c.logs(s.ctx, s.job, s.offset, s.Snapshot)
			if err != nil {
				var refusal *Error
				if errors.As(err, &refusal) {
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
			s.body = body
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
