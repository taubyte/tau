package websiteI18n_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	"gotest.tools/v3/assert"
)

func TestHelp_BeSureToCloneWebsite(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Help().BeSureToCloneWebsite()
	assert.Assert(t, strings.Contains(buf.String(), "clone the website"))
	assert.Assert(t, strings.Contains(buf.String(), "tau clone website"))
}

func TestHelp_WebsiteAlreadyCloned(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	websiteI18n.Help().WebsiteAlreadyCloned("/path/to/dir")
	assert.Assert(t, strings.Contains(buf.String(), "already cloned"))
	assert.Assert(t, strings.Contains(buf.String(), "/path/to/dir"))
}
