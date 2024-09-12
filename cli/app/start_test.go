package app

import (
	"context"
	"os"
	"testing"
	"time"

	_ "embed"

	"gotest.tools/v3/assert"
)

//go:embed fixtures/testConfig.yaml
var testConfig []byte

//go:embed fixtures/test_swarm.key
var testSwarmKey []byte

//go:embed fixtures/test.key
var testKey []byte

// TODO: add hoarder to config when its fixed
// TODO: Build in tmp
func TestStart(t *testing.T) {
	app := newApp()

	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	root := t.TempDir()

	os.Mkdir(root+"/storage", 0750)
	os.Mkdir(root+"/storage/test", 0750)
	os.Mkdir(root+"/config", 0750)
	os.Mkdir(root+"/config/keys", 0750)

	os.WriteFile(root+"/config/test.yaml", testConfig, 0640)
	os.WriteFile(root+"/config/keys/test_swarm.key", testSwarmKey, 0640)
	os.WriteFile(root+"/config/keys/test.key", testKey, 0640)

	err := app.RunContext(ctx, []string{os.Args[0], "start", "-s", "test", "--root", root, "--dev"})
	assert.NilError(t, err)
}
