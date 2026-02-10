package serviceI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	serviceI18n "github.com/taubyte/tau/tools/tau/i18n/service"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	serviceI18n.Created("mysvc")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "service"))
	assert.Assert(t, strings.Contains(buf.String(), "mysvc"))
}

func TestDeleted(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	serviceI18n.Deleted("mysvc")
	assert.Assert(t, strings.Contains(buf.String(), "Deleted"))
	assert.Assert(t, strings.Contains(buf.String(), "mysvc"))
}

func TestEdited(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	serviceI18n.Edited("mysvc")
	assert.Assert(t, strings.Contains(buf.String(), "Edited"))
	assert.Assert(t, strings.Contains(buf.String(), "mysvc"))
}
