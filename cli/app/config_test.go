package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	_ "embed"

	"github.com/taubyte/tau/config"
	"gotest.tools/v3/assert"
)

func TestConfig(t *testing.T) {
	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	root := t.TempDir()

	fmt.Println("ROOT ", root)
	os.Mkdir(root+"/storage", 0750)
	os.Mkdir(root+"/storage/test", 0750)
	os.Mkdir(root+"/config", 0750)
	os.Mkdir(root+"/config/keys", 0750)

	err := newApp().RunContext(ctx, []string{os.Args[0], "--root", root, "cnf", "gen", "-s", "test", "--services", "auth,seer,monkey", "--swarm-key", "--dv-keys"})
	assert.NilError(t, err)

	err = newApp().RunContext(ctx, []string{os.Args[0], "--root", root, "cnf", "ok?", "-s", "test"})
	assert.NilError(t, err)

	err = newApp().RunContext(ctx, []string{os.Args[0], "--root", root, "cnf", "show", "-s", "test"})
	assert.NilError(t, err)

	config.DefaultRoot = root
	err = newApp().RunContext(ctx, []string{os.Args[0], "cnf", "show", "-s", "test"})
	assert.NilError(t, err)
}
