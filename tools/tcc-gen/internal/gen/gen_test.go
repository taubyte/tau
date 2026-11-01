package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestBothTypeScriptCopiesAreGeneratedAndChecked(t *testing.T) {
	generated, err := Generate(schema.GenerationRoot())
	if err != nil {
		t.Fatal(err)
	}
	explorer, client := filepath.FromSlash(tsGenPath), filepath.FromSlash(clientTSPath)
	if len(generated[explorer]) == 0 || string(generated[explorer]) != string(generated[client]) {
		t.Fatalf("the client's schema.ts is not the explorer's: %d and %d bytes", len(generated[explorer]), len(generated[client]))
	}

	dir := t.TempDir()
	if err := WriteTo(dir, generated); err != nil {
		t.Fatal(err)
	}
	drifted, err := Check(dir, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifted) != 0 {
		t.Fatalf("what was just written drifted: %v", drifted)
	}

	if err := os.WriteFile(filepath.Join(dir, client), append(generated[client], '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, err = Check(dir, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifted) != 1 || drifted[0] != client {
		t.Fatalf("drifted = %v, want only %s", drifted, client)
	}

	var report strings.Builder
	PrintReport(&report, generated, drifted)
	for _, want := range []string{"OK    " + explorer, "DIFF  " + client, "5 generated files checked, 1 with differences"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, report.String())
		}
	}
}
