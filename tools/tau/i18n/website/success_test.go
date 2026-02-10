package websiteI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Created("mywebsite")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "website"))
	assert.Assert(t, strings.Contains(buf.String(), "mywebsite"))
}

func TestRegistered(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Registered("https://example.com/repo")
	assert.Assert(t, strings.Contains(buf.String(), "Registered"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestPulled(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Pulled("https://example.com/repo")
	assert.Assert(t, strings.Contains(buf.String(), "Pulled"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestPushed(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Pushed("https://example.com/repo", "fix: thing")
	assert.Assert(t, strings.Contains(buf.String(), "Pushed"))
	assert.Assert(t, strings.Contains(buf.String(), "fix: thing"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}

func TestCheckedOut(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.CheckedOut("https://example.com/repo", "main")
	assert.Assert(t, strings.Contains(buf.String(), "Checked out"))
	assert.Assert(t, strings.Contains(buf.String(), "main"))
	assert.Assert(t, strings.Contains(buf.String(), "https://example.com/repo"))
}
