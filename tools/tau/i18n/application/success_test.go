package applicationI18n_test

import (
	"bytes"
	"strings"
	"testing"

	applicationI18n "github.com/taubyte/tau/tools/tau/i18n/application"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestSelected(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	applicationI18n.Selected("myapp")
	assert.Assert(t, strings.Contains(buf.String(), "Selected"))
	assert.Assert(t, strings.Contains(buf.String(), "application"))
	assert.Assert(t, strings.Contains(buf.String(), "myapp"))
}

func TestDeselected(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	applicationI18n.Deselected("myapp")
	assert.Assert(t, strings.Contains(buf.String(), "Deselected"))
	assert.Assert(t, strings.Contains(buf.String(), "myapp"))
}

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	applicationI18n.Created("myapp")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "myapp"))
}

func TestDeleted(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	applicationI18n.Deleted("myapp")
	assert.Assert(t, strings.Contains(buf.String(), "Deleted"))
	assert.Assert(t, strings.Contains(buf.String(), "myapp"))
}

func TestEdited(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	applicationI18n.Edited("myapp")
	assert.Assert(t, strings.Contains(buf.String(), "Edited"))
	assert.Assert(t, strings.Contains(buf.String(), "myapp"))
}
