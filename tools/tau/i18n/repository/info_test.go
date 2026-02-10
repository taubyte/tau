package repositoryI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	"gotest.tools/v3/assert"
)

func TestTriggerBuild(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	repositoryI18n.TriggerBuild()
	assert.Assert(t, strings.Contains(buf.String(), "Trigger build"))
	assert.Assert(t, strings.Contains(buf.String(), "tau push"))
}

func TestImported(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	repositoryI18n.Imported("myrepo", "sandbox.taubyte.com")
	assert.Assert(t, strings.Contains(buf.String(), "Imported"))
	assert.Assert(t, strings.Contains(buf.String(), "repository"))
	assert.Assert(t, strings.Contains(buf.String(), "myrepo"))
	assert.Assert(t, strings.Contains(buf.String(), "sandbox.taubyte.com"))
}
