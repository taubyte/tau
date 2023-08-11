package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	_ "embed"

	"github.com/taubyte/tau/config"
	"gotest.tools/v3/assert"
)

func TestConfig(t *testing.T) {
	app := newApp()

	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	root, err := os.MkdirTemp("/tmp", "tau-test")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)

	fmt.Println("ROOT ", root)
	os.Mkdir(root+"/storage", 0750)
	os.Mkdir(root+"/storage/test", 0750)
	os.Mkdir(root+"/config", 0750)
	os.Mkdir(root+"/config/keys", 0750)

	err = app.RunContext(ctx, []string{os.Args[0], "cnf", "gen", "-s", "test", "--root", root, "--protos", "auth,seer,monkey", "--swarm-key", "--dv-keys"})
	assert.NilError(t, err)

	err = app.RunContext(ctx, []string{os.Args[0], "cnf", "ok?", "-s", "test", "--root", root})
	assert.NilError(t, err)

	err = app.RunContext(ctx, []string{os.Args[0], "cnf", "show", "-s", "test", "--root", root})
	assert.NilError(t, err)

	config.DefaultRoot = root
	err = app.RunContext(ctx, []string{os.Args[0], "cnf", "show", "-s", "test"})
	assert.NilError(t, err)
}
