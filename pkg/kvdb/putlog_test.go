package kvdb

import (
	"fmt"
	"strings"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// A write is logged by key and size; the value, a deploy key say, never.
func TestPutLogLineOmitsValue(t *testing.T) {
	secret := "deploy-key-material:do-not-log:5f1c9a"
	line := putLogLine(ds.NewKey("/repositories/github/1/key"), []byte(secret))

	if strings.Contains(line, "do-not-log") {
		t.Fatalf("value logged: %q", line)
	}
	if !strings.Contains(line, "/repositories/github/1/key") || !strings.Contains(line, fmt.Sprintf("(%d bytes)", len(secret))) {
		t.Fatalf("key or size missing: %q", line)
	}
}
