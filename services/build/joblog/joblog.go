// Package joblog is a run's log as both halves of the build read it: its events,
// each a length-delimited Job.Event (protodelim) — its status as it moves and each
// line of its output — written by the backend while the run goes on and stored,
// compressed, when it ends. So the stored log is everything that happened to that
// run, and a follower reads the same records either way.
package joblog

import (
	"bufio"
	"bytes"
	"io"

	"google.golang.org/protobuf/encoding/protodelim"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// Write appends one event to a log, in one write, so a reader of a log being
// written never meets half a record it has to guess the rest of.
func Write(w io.Writer, ev *buildv2.Job_Event) error {
	var record bytes.Buffer
	if _, err := protodelim.MarshalTo(&record, ev); err != nil {
		return err
	}
	_, err := w.Write(record.Bytes())
	return err
}

// Events reads a log's events from a byte offset that is a record's start.
type Events struct {
	r *counted
}

// Read is the events of r, which starts at offset in its log.
func Read(r io.Reader, offset int64) *Events {
	return &Events{r: &counted{r: bufio.NewReader(r), n: offset}}
}

// Next is the next event, carrying the offset after it: io.EOF at the end of the
// log, io.ErrUnexpectedEOF in a record the log was cut in.
func (e *Events) Next() (*buildv2.Job_Event, error) {
	ev := &buildv2.Job_Event{}
	start := e.r.n
	if err := protodelim.UnmarshalFrom(e.r, ev); err != nil {
		if err == io.EOF && e.r.n != start {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	ev.Offset = e.r.n
	return ev, nil
}

// Offset is the byte after the last event read.
func (e *Events) Offset() int64 { return e.r.n }

// counted is a buffered log that knows how far into it a read has gone.
type counted struct {
	r *bufio.Reader
	n int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *counted) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// Terminal is whether a status ends a job, and so ends its last run's log.
func Terminal(status buildv2.JobStatus) bool {
	switch status {
	case buildv2.JobStatus_JobStatusSuccess, buildv2.JobStatus_JobStatusFailed, buildv2.JobStatus_JobStatusCancelled:
		return true
	}
	return false
}
