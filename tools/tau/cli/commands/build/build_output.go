package build

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/taubyte/tau/tools/tau/prompts/spinner"
)

// buildEvent represents a JSON event from pkg/builder (op, step, status, etc.).
type buildEvent struct {
	Op        string            `json:"op"`
	Image     string            `json:"image"`
	Step      string            `json:"step"`
	Status    string            `json:"status"`
	Error     string            `json:"error"`
	Env       map[string]string `json:"env"`
	Success   bool              `json:"success"`
	Timestamp int64             `json:"timestamp"`
}

// buildOutputWriter wraps dest and turns builder JSON events into section headers
// and spinner updates; all other lines are passed through to dest.
type buildOutputWriter struct {
	dest   io.Writer
	buf    *bufio.Writer
	line   []byte
	update func(string)
	stop   func()
}

// NewBuildOutputWriter returns an io.Writer that parses JSON build events and
// renders sections with a spinner; non-JSON lines are written unchanged to dest.
func NewBuildOutputWriter(dest io.Writer) io.Writer {
	return &buildOutputWriter{
		dest: dest,
		buf:  bufio.NewWriter(dest),
		line: make([]byte, 0, 512),
	}
}

// writeOut writes s to the buffer and flushes; errors are ignored (buffer to terminal is best-effort).
func (w *buildOutputWriter) writeOut(s string) {
	w.buf.WriteString(s)
	w.buf.Flush()
}

func (w *buildOutputWriter) flushLine() error {
	if len(w.line) == 0 {
		return nil
	}
	line := string(w.line)
	w.line = w.line[:0]

	// If we have an active spinner, stop it before writing so the line is visible
	if w.stop != nil {
		w.stop()
		w.update, w.stop = nil, nil
	}

	// Try to parse as JSON build event
	var ev buildEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		w.writeOut(line + "\n")
		return nil
	}

	// Handle known events (suppress raw JSON, render sections + spinner)
	switch {
	case ev.Op == "pull/build image":
		w.update, w.stop = spinner.StartWithSuffix(" Pull/build image...")
		if ev.Image != "" {
			w.writeOut("image: " + ev.Image + "\n")
		}
	case ev.Op == "run container":
		if w.update != nil {
			w.update(" Run container...")
		} else {
			w.update, w.stop = spinner.StartWithSuffix(" Run container...")
		}
	case ev.Step != "" && ev.Status == "":
		if w.update != nil {
			w.update(" Step: " + ev.Step + "...")
		} else {
			w.update, w.stop = spinner.StartWithSuffix(" Step: " + ev.Step + "...")
		}
	case ev.Step != "" && ev.Status == "success":
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		w.writeOut("Step: " + ev.Step + " — success\n")
	case ev.Step != "" && ev.Status == "error":
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		w.writeOut("Step: " + ev.Step + " — error\n")
		if ev.Error != "" {
			w.writeOut(ev.Error + "\n")
		}
	case ev.Error != "":
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		w.writeOut("build failed: " + ev.Error + "\n")
	case ev.Success:
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		w.writeOut("Done\n")
	default:
		w.writeOut(line + "\n")
	}
	return nil
}

func (w *buildOutputWriter) Write(p []byte) (n int, err error) {
	for _, b := range p {
		n++
		if b == '\n' {
			if err = w.flushLine(); err != nil {
				return n - 1, err
			}
			continue
		}
		w.line = append(w.line, b)
	}
	return n, nil
}

// Close stops any active spinner and flushes remaining buffered line (no trailing newline).
// Callers that use this as the builder output should call Close when the build finishes
// so the final state is consistent. If the builder always ends with a newline, the last
// line is already flushed in Write.
func (w *buildOutputWriter) Close() error {
	if w.stop != nil {
		w.stop()
		w.update, w.stop = nil, nil
	}
	if len(w.line) != 0 {
		w.writeOut(string(w.line) + "\n")
		w.line = w.line[:0]
	}
	return w.buf.Flush()
}
