package projectI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"gotest.tools/v3/assert"
)

func TestDeselectedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.DeselectedProject("myproject")
	assert.Assert(t, strings.Contains(buf.String(), "Deselected"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestSelectedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.SelectedProject("myproject")
	assert.Assert(t, strings.Contains(buf.String(), "Selected"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestCreatedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.CreatedProject("myproject")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestPushedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.PushedProject("myproject")
	assert.Assert(t, strings.Contains(buf.String(), "Pushed"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestPulledProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.PulledProject("myproject")
	assert.Assert(t, strings.Contains(buf.String(), "Pulled"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestCheckedOutProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.CheckedOutProject("myproject", "main")
	assert.Assert(t, strings.Contains(buf.String(), "Checked out"))
	assert.Assert(t, strings.Contains(buf.String(), "main"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
}

func TestImportedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.ImportedProject("myproject", "sandbox.taubyte.com")
	assert.Assert(t, strings.Contains(buf.String(), "Imported"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
	assert.Assert(t, strings.Contains(buf.String(), "sandbox.taubyte.com"))
}

func TestRemovedProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	projectI18n.RemovedProject("myproject", "sandbox.taubyte.com")
	assert.Assert(t, strings.Contains(buf.String(), "Removed"))
	assert.Assert(t, strings.Contains(buf.String(), "myproject"))
	assert.Assert(t, strings.Contains(buf.String(), "sandbox.taubyte.com"))
}
