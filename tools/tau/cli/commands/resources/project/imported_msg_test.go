package project

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/session"
)

func TestImportedMessageNamesSelectedCloud(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := session.Set().CustomCloudUrl("blackhole"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	defer printer.SetOutput(printer.WriterOutput(&buf))()
	reportImported("p1")
	if !strings.Contains(buf.String(), "blackhole") {
		t.Fatalf("message: %q", buf.String())
	}
}
