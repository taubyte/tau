package dreamI18n_test

import (
	"bytes"
	"strings"
	"testing"

	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestHelp_IsAValidBinary(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	h := dreamI18n.Help()
	h.IsAValidBinary()
	assert.Assert(t, strings.Contains(buf.String(), "dream"))
	assert.Assert(t, strings.Contains(buf.String(), "valid binary"))
}

func TestHelp_IsDreamRunning(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	h := dreamI18n.Help()
	h.IsDreamRunning()
	assert.Assert(t, strings.Contains(buf.String(), "Have you started dream"))
	assert.Assert(t, strings.Contains(buf.String(), "tau dream"))
}
