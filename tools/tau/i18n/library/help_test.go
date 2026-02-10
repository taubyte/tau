package libraryI18n_test

import (
	"bytes"
	"strings"
	"testing"

	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestHelp_BeSureToCloneLibrary(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Help().BeSureToCloneLibrary()
	assert.Assert(t, strings.Contains(buf.String(), "clone the library"))
	assert.Assert(t, strings.Contains(buf.String(), "tau clone library"))
}

func TestHelp_LibraryAlreadyCloned(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	libraryI18n.Help().LibraryAlreadyCloned("/path/to/dir")
	assert.Assert(t, strings.Contains(buf.String(), "already cloned"))
	assert.Assert(t, strings.Contains(buf.String(), "/path/to/dir"))
}
