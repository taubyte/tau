package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	odo "bitbucket.org/taubyte/odo"
	"gotest.tools/v3/assert"
)

var (
	shape = "test"
	ports = []int{8100, 8102, 8104}
)

func TestStart(t *testing.T) {
	app, err := Build()
	assert.NilError(t, err)

	defer os.RemoveAll(shape + odo.ClientPrefix)
	defer os.RemoveAll(shape)

	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*2)
	defer ctxC()

	err = app.RunContext(ctx, append(
		os.Args[0:1],
		[]string{"start", "-s", "test", "-c", "testConfig.yaml"}...),
	)
	assert.NilError(t, err)

	// err = checkPorts()
	// assert.NilError(t, err)

	read := captureStdOut()
	out := read()
	expectedToContain := fmt.Sprintf("%s started", shape)
	assert.Assert(t, strings.Contains(out, expectedToContain), fmt.Errorf("expected %s to contain %s", out, expectedToContain))
}

// https://stackoverflow.com/questions/10473800/in-go-how-do-i-capture-stdout-of-a-function-into-a-string
func captureStdOut() (read func() string) {
	rescueStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	return func() string {
		w.Close()
		out, _ := io.ReadAll(r)
		os.Stdout = rescueStdout

		return string(out)
	}
}

func checkPorts() error {
	// Make sure nothing is starting on set ports
	for _, port := range ports {
		address := fmt.Sprintf("localhost:%d", port)

		conn, err := net.Dial("tcp", address)
		if err != nil {
			return err
		}
		defer conn.Close()
	}

	return nil
}
