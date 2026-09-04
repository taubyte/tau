package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/node/boot"
)

// THERE IS ONE START AND IT IS THE NODE. The test this replaces booted a v1
// shape from a v1 config; v1 is not a target, and a command named for a version
// is a command that says both are supported.

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	app := newApp()
	var out bytes.Buffer
	app.Writer = &out

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := app.RunContext(ctx, append([]string{os.Args[0]}, args...))
	return out.String(), err
}

// TestConfigPrintsSomethingStartAccepts. The first question an operator has is
// what the file looks like, and the answer must not drift from the struct that
// reads it.
func TestConfigRoundTripsIntoStart(t *testing.T) {
	printed, err := run(t, "config")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(printed, "services:"), printed)
	assert.Assert(t, strings.Contains(printed, "swarmkey:"), printed)

	// It prints a PLACEHOLDER for the swarm key rather than one that works, so
	// the example cannot quietly become a fleet everybody shares — and reading
	// it says which line to fill in.
	path := filepath.Join(t.TempDir(), "node.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(printed), 0o644))

	_, err = boot.Read(path)
	assert.ErrorContains(t, err, "swarmkey")

	// With a real key it reads, which is the other half: the example is a
	// file, not a sketch of one.
	filled := strings.Replace(printed,
		"<base64; a node without one joins the public network>",
		base64.StdEncoding.EncodeToString(swarmKey()), 1)
	assert.NilError(t, os.WriteFile(path, []byte(filled), 0o644))

	file, err := boot.Read(path)
	assert.NilError(t, err)
	assert.Assert(t, len(file.Node.Services) > 0)
}

// TestStartRunsANodeAndStopsWithItsContext.
func TestStart(t *testing.T) {
	root := t.TempDir()

	config := "" +
		"node:\n" +
		"  services: [register, stats]\n" +
		"  cloud: test\n" +
		"root: " + filepath.Join(root, "data") + "\n" +
		"listen: [\"/ip4/127.0.0.1/tcp/0\"]\n" +
		"swarmkey: " + base64.StdEncoding.EncodeToString(swarmKey()) + "\n"

	path := filepath.Join(root, "node.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(config), 0o644))

	app := newApp()

	// The command runs until its context ends, which is what a node does.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- app.RunContext(ctx, []string{os.Args[0], "start", path}) }()

	select {
	case err := <-done:
		assert.NilError(t, err)
	case <-time.After(50 * time.Second):
		t.Fatal("start never returned after its context ended")
	}
}

func TestStartRefusesWhatItCannotRun(t *testing.T) {
	_, err := run(t, "start")
	assert.ErrorContains(t, err, "one config file")

	_, err = run(t, "start", filepath.Join(t.TempDir(), "nothing-here.yaml"))
	assert.Assert(t, err != nil)

	// A NODE WITH NO SWARM KEY WOULD JOIN THE PUBLIC NETWORK, and every v2
	// service trusts its swarm.
	path := filepath.Join(t.TempDir(), "open.yaml")
	assert.NilError(t, os.WriteFile(path, []byte("root: /tmp/x\n"), 0o644))

	_, err = run(t, "start", path)
	assert.ErrorContains(t, err, "swarmkey")
}

func swarmKey() []byte {
	return []byte("/key/swarm/psk/1.0.0/\n/base16/\n" +
		"0000000000000000000000000000000000000000000000000000000000000001")
}
