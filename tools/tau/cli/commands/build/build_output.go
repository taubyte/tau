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
		_, err = w.buf.WriteString(line + "\n")
		if err != nil {
			return err
		}
		return w.buf.Flush()
	}

	// Handle known events (suppress raw JSON, render sections + spinner)
	switch {
	case ev.Op == "pull/build image":
		w.update, w.stop = spinner.StartWithSuffix(" Pull/build image...")
		if ev.Image != "" {
			_, _ = w.buf.WriteString("image: " + ev.Image + "\n")
			_ = w.buf.Flush()
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
		_, _ = w.buf.WriteString("Step: " + ev.Step + " — success\n")
		_ = w.buf.Flush()
	case ev.Step != "" && ev.Status == "error":
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		_, _ = w.buf.WriteString("Step: " + ev.Step + " — error\n")
		if ev.Error != "" {
			_, _ = w.buf.WriteString(ev.Error + "\n")
		}
		_ = w.buf.Flush()
	case ev.Error != "":
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		_, _ = w.buf.WriteString("build failed: " + ev.Error + "\n")
		_ = w.buf.Flush()
	case ev.Success:
		if w.stop != nil {
			w.stop()
			w.update, w.stop = nil, nil
		}
		_, _ = w.buf.WriteString("Done\n")
		_ = w.buf.Flush()
	default:
		// Unknown JSON: pass through
		_, _ = w.buf.WriteString(line + "\n")
		_ = w.buf.Flush()
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
		_, _ = w.buf.WriteString(string(w.line) + "\n")
		w.line = w.line[:0]
	}
	return w.buf.Flush()
}
