package loginI18n_test

import (
	"bytes"
	"strings"
	"testing"

	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestCreated(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	loginI18n.Created("github")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "profile"))
	assert.Assert(t, strings.Contains(buf.String(), "github"))
}

func TestCreatedDefault(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	loginI18n.CreatedDefault("github")
	assert.Assert(t, strings.Contains(buf.String(), "Created default"))
	assert.Assert(t, strings.Contains(buf.String(), "github"))
}

func TestSelected(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	loginI18n.Selected("github")
	assert.Assert(t, strings.Contains(buf.String(), "Selected"))
	assert.Assert(t, strings.Contains(buf.String(), "github"))
}
