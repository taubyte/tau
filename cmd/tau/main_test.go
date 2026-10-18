package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/boot"
)

func swarmKey() string {
	return base64.StdEncoding.EncodeToString([]byte("/key/swarm/psk/1.0.0/\n/base16/\n" +
		"0000000000000000000000000000000000000000000000000000000000000001"))
}

func runApp(t *testing.T, args ...string) (string, error) {
	t.Helper()
	app := newApp(nil)
	var out bytes.Buffer
	app.Writer = &out
	err := app.RunContext(t.Context(), append([]string{"tau"}, args...))
	return out.String(), err
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestVersion(t *testing.T) {
	out, err := runApp(t, "version")
	require.NoError(t, err)
	require.Equal(t, "tau dev (none, unknown)\n", out)
}

// TestConfigPrintsSomethingStartAccepts once its placeholder key is filled.
func TestConfigRoundTripsIntoRead(t *testing.T) {
	printed, err := runApp(t, "config")
	require.NoError(t, err)

	path := writeConfig(t, printed)
	_, err = boot.Read(path)
	require.ErrorContains(t, err, "swarmkey", "the placeholder is not a key")

	path = writeConfig(t, strings.Replace(printed, "<base64; a node without one joins the public network>", swarmKey(), 1))
	file, err := boot.Read(path)
	require.NoError(t, err)
	require.NotEmpty(t, file.Node.Services)
}

func TestStartRefusesWhatItCannotRun(t *testing.T) {
	_, err := runApp(t, "start")
	require.ErrorContains(t, err, "config")

	_, err = runApp(t, "start", "--config", filepath.Join(t.TempDir(), "nothing-here.yaml"))
	require.Error(t, err)

	_, err = runApp(t, "start", "--config", writeConfig(t, "root: /tmp/x\n"))
	require.ErrorContains(t, err, "swarmkey")

	// A file that reads but a node that cannot start: the listen address is
	// not a multiaddr.
	_, err = runApp(t, "start", "--config", writeConfig(t, "open: true\nlisten: [nonsense]\n"))
	require.Error(t, err)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestPrintExampleReportsAWriteFailure(t *testing.T) {
	require.ErrorIs(t, printExample(failingWriter{}), os.ErrClosed)
}

// TestStartServesTheGatewayAndStops runs the command the way systemd does —
// a config file and a root — sees the gateway answer on loopback, and stops it
// first with its context and then with a signal.
func TestStartServesTheGatewayAndStops(t *testing.T) {
	config := writeConfig(t, ""+
		"node:\n"+
		"  services: [register, stats, gateway]\n"+
		"  cloud: test\n"+
		"root: /nonexistent/overridden\n"+
		"listen: [\"/ip4/127.0.0.1/tcp/0\"]\n"+
		"swarmkey: "+swarmKey()+"\n"+
		"http: 127.0.0.1:0\n")

	for _, stopWith := range []string{"context", "signal"} {
		t.Run(stopWith, func(t *testing.T) {
			root := t.TempDir()
			addresses := make(chan string, 1)
			app := newApp(func(r *boot.Running) { addresses <- r.Address() })

			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- app.RunContext(ctx, []string{"tau", "start", "--config", config, "--root", root}) }()

			var address string
			select {
			case address = <-addresses:
			case err := <-done:
				t.Fatalf("start returned before serving: %v", err)
			}

			res, err := http.Get("http://" + address + "/")
			require.NoError(t, err)
			res.Body.Close()
			require.NotZero(t, res.StatusCode, "the gateway answered")

			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.NotEmpty(t, entries, "--root is where the node keeps its data")

			if stopWith == "context" {
				cancel()
			} else {
				self, err := os.FindProcess(os.Getpid())
				require.NoError(t, err)
				require.NoError(t, self.Signal(os.Interrupt))
			}
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(30 * time.Second):
				t.Fatal("start did not return after being stopped")
			}
		})
	}
}

func TestRunIsMainsExitStatus(t *testing.T) {
	var out, errs bytes.Buffer
	require.Equal(t, 0, run([]string{"tau", "version"}, &out, &errs))
	require.Contains(t, out.String(), "tau dev")

	out.Reset()
	require.Equal(t, 1, run([]string{"tau", "start"}, &out, &errs))
	require.Contains(t, errs.String(), "config")
}
