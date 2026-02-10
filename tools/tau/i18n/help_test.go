package i18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestHelp_HaveYouLoggedIn(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	i18n.Help().HaveYouLoggedIn()
	assert.Assert(t, strings.Contains(buf.String(), "Have you logged in"))
	assert.Assert(t, strings.Contains(buf.String(), "tau login"))
}

func TestHelp_HaveYouSelectedACloud(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	i18n.Help().HaveYouSelectedACloud()
	assert.Assert(t, strings.Contains(buf.String(), "selected a cloud"))
	assert.Assert(t, strings.Contains(buf.String(), "tau select cloud"))
}

func TestHelp_TokenMayBeExpired(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	i18n.Help().TokenMayBeExpired("github")
	assert.Assert(t, strings.Contains(buf.String(), "Token may be expired"))
	assert.Assert(t, strings.Contains(buf.String(), "github"))
	assert.Assert(t, strings.Contains(buf.String(), "tau login"))
}

func TestHelp_BeSureToCloneProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	i18n.Help().BeSureToCloneProject()
	assert.Assert(t, strings.Contains(buf.String(), "clone the project"))
	assert.Assert(t, strings.Contains(buf.String(), "tau clone project"))
}

func TestHelp_BeSureToSelectProject(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	i18n.Help().BeSureToSelectProject()
	assert.Assert(t, strings.Contains(buf.String(), "selected a project"))
	assert.Assert(t, strings.Contains(buf.String(), "tau select project"))
}
