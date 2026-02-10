package printer_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"gotest.tools/v3/assert"
)

func TestSuccessWithName(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	printer.SuccessWithName("%s item: %s", "Created", "foo")
	assert.Assert(t, strings.Contains(buf.String(), "Created"))
	assert.Assert(t, strings.Contains(buf.String(), "foo"))
}

func TestSuccessWithNameOnCloud(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()
	printer.SuccessWithNameOnCloud("%s project: %s on cloud: %s", "Imported", "proj", "cloud.example.com")
	assert.Assert(t, strings.Contains(buf.String(), "Imported"))
	assert.Assert(t, strings.Contains(buf.String(), "proj"))
	assert.Assert(t, strings.Contains(buf.String(), "cloud.example.com"))
}

func TestNoop(t *testing.T) {
	restore := printer.SetOutput(printer.Noop())
	defer restore()

	printer.Out.InfoPrintln("should not appear")
	printer.Out.InfoPrintfln("format %s", "args")
	printer.Out.SuccessPrintfln("success %s", "x")
	printer.Out.WarningPrintln("warn")
	printer.Out.WarningPrintfln("warn %s", "fmt")
	printer.Out.Warning(nil)
	printer.Out.Warning(errors.New("noop err"))
	assert.Equal(t, printer.Out.SprintCyan("x"), "x")
	assert.Equal(t, printer.Out.SprintfGreen("y"), "y")
}

func TestWriterOutput(t *testing.T) {
	var buf bytes.Buffer
	restore := printer.SetOutput(printer.WriterOutput(&buf))
	defer restore()

	printer.Out.InfoPrintln("info line")
	printer.Out.InfoPrintfln("format %s", "arg")
	printer.Out.SuccessPrintfln("ok %s", "name")
	printer.Out.WarningPrintfln("warning %s", "msg")
	printer.Out.Warning(errors.New("err message"))

	assert.Assert(t, bytes.Contains(buf.Bytes(), []byte("info line")))
	assert.Assert(t, bytes.Contains(buf.Bytes(), []byte("format arg")))
	assert.Assert(t, bytes.Contains(buf.Bytes(), []byte("ok name")))
	assert.Assert(t, bytes.Contains(buf.Bytes(), []byte("warning msg")))
	assert.Assert(t, bytes.Contains(buf.Bytes(), []byte("err message")))
}
