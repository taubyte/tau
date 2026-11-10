package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/tools/dream/cli/common"
)

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"dream", "--version"}, {"dream", "version"}} {
		app := defineCLI(&common.Context{Ctx: t.Context()})
		var out bytes.Buffer
		app.Writer = &out
		if err := app.Run(args); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != dream.BuildVersion().String() || !strings.HasPrefix(got, "dream ") {
			t.Fatalf("%v: %q", args, got)
		}
	}
}
