package libraryI18n_test

import (
	"bytes"
	"strings"
	"testing"

	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Created("mylib")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "library"))
	assert.Assert(t, strings.Contains(buf.String(), "mylib"))
}

func TestRegistered(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Registered("https://example.com/repo")
	assert.Assert(t, strings.Contains(buf.String(), "Registered"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestPulled(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Pulled("https://example.com/repo")
	assert.Assert(t, strings.Contains(buf.String(), "Pulled"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestPushed(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Pushed("https://example.com/repo", "fix: thing")
	assert.Assert(t, strings.Contains(buf.String(), "Pushed"))
	assert.Assert(t, strings.Contains(buf.String(), "fix: thing"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestCheckedOut(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.CheckedOut("https://example.com/repo", "main")
	assert.Assert(t, strings.Contains(buf.String(), "Checked out"))
	assert.Assert(t, strings.Contains(buf.String(), "main"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}
