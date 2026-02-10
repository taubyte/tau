package messagingI18n_test

import (
	"bytes"
	"strings"
	"testing"

	messagingI18n "github.com/taubyte/tau/tools/tau/i18n/messaging"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	messagingI18n.Created("mymsg")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "messaging"))
	assert.Assert(t, strings.Contains(buf.String(), "mymsg"))
}

func TestDeleted(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	messagingI18n.Deleted("mymsg")
	assert.Assert(t, strings.Contains(buf.String(), "Deleted"))
	assert.Assert(t, strings.Contains(buf.String(), "mymsg"))
}

func TestEdited(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	messagingI18n.Edited("mymsg")
	assert.Assert(t, strings.Contains(buf.String(), "Edited"))
	assert.Assert(t, strings.Contains(buf.String(), "mymsg"))
}
