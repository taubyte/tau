package storageI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	storageI18n "github.com/taubyte/tau/tools/tau/i18n/storage"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	storageI18n.Created("mystore")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "storage"))
	assert.Assert(t, strings.Contains(buf.String(), "mystore"))
}

func TestDeleted(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	storageI18n.Deleted("mystore")
	assert.Assert(t, strings.Contains(buf.String(), "Deleted"))
	assert.Assert(t, strings.Contains(buf.String(), "mystore"))
}

func TestEdited(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	storageI18n.Edited("mystore")
	assert.Assert(t, strings.Contains(buf.String(), "Edited"))
	assert.Assert(t, strings.Contains(buf.String(), "mystore"))
}
