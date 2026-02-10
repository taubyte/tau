package functionI18n_test

import (
	"bytes"
	"strings"
	"testing"

	functionI18n "github.com/taubyte/tau/tools/tau/i18n/function"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	functionI18n.Created("myfunc")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "function"))
	assert.Assert(t, strings.Contains(buf.String(), "myfunc"))
}

func TestDeleted(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	functionI18n.Deleted("myfunc")
	assert.Assert(t, strings.Contains(buf.String(), "Deleted"))
	assert.Assert(t, strings.Contains(buf.String(), "myfunc"))
}

func TestEdited(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	functionI18n.Edited("myfunc")
	assert.Assert(t, strings.Contains(buf.String(), "Edited"))
	assert.Assert(t, strings.Contains(buf.String(), "myfunc"))
}
