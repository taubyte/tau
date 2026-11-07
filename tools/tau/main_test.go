package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// The user sees the friendly sentence only: no log date, no "command failed with".
func TestFailPrintsNoLogPrefix(t *testing.T) {
	var out bytes.Buffer
	fail(&out, errors.New("Dream has no universe \"x\".\n  hint: start it"))
	s := out.String()
	if strings.Contains(s, "command failed") || strings.HasPrefix(s, "20") || !strings.HasPrefix(s, "Dream has no universe") {
		t.Fatalf("got %q", s)
	}
}
