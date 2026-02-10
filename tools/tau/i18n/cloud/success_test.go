package cloudI18n_test

import (
	"bytes"
	"strings"
	"testing"

	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestSuccess(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	cloudI18n.Success("sandbox.taubyte.com")
	assert.Assert(t, strings.Contains(buf.String(), "Connected to"))
	assert.Assert(t, strings.Contains(buf.String(), "sandbox.taubyte.com"))
}
