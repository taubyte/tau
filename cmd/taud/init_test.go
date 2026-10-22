package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/node/boot"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	secretsService "github.com/taubyte/tau/services/secrets"
)

func secretsConfig(t *testing.T) string {
	t.Helper()
	key, root, cert := admitted(t, "test")
	return writeConfig(t, ""+
		"node:\n"+
		"  services: [register, discovery, secrets]\n"+
		"  cloud: test\n"+
		"root: "+t.TempDir()+"\n"+
		"listen: [\"/ip4/127.0.0.1/tcp/0\"]\n"+
		"privatekey: "+key+"\n"+
		"ca: "+quoted(root)+"\n"+
		"certificate: "+quoted(cert)+"\n")
}

func sealedBundle(t *testing.T, project, value string) []byte {
	t.Helper()
	sealed, err := secretsService.Sealed(secretsService.Ref{Scope: secretsService.Project(project, "e"), Name: "n"}, []byte(value), time.Now())
	require.NoError(t, err)
	data, err := proto.Marshal(&secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}})
	require.NoError(t, err)
	return data
}

// TestInitWritesWhatStartThenServes: the bundle arrives on stdin, as
// spore-drive streams it, and from a file; the node then serves both.
func TestInitWritesWhatStartThenServes(t *testing.T) {
	config := secretsConfig(t)

	app := newApp(nil)
	app.Reader = bytes.NewReader(sealedBundle(t, "from-stdin", "a"))
	require.NoError(t, app.RunContext(t.Context(), []string{"taud", "init", "--config", config, "-"}))

	file := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, os.WriteFile(file, sealedBundle(t, "from-file", "b"), 0o600))
	_, err := runApp(t, "init", "--config", config, file)
	require.NoError(t, err)

	running := make(chan *boot.Running, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- newApp(func(r *boot.Running) { running <- r }).RunContext(ctx, []string{"taud", "start", "--config", config})
	}()

	var r *boot.Running
	select {
	case r = <-running:
	case err := <-done:
		t.Fatalf("start returned before serving: %v", err)
	}
	for project, want := range map[string]string{"from-stdin": "a", "from-file": "b"} {
		require.Eventually(t, func() bool {
			got, err := r.Node.Vault().Secret(ctx, project, "e", "n")
			return err == nil && got == want
		}, 30*time.Second, 50*time.Millisecond, project)
	}
	cancel()
	require.NoError(t, <-done)
}

func TestInitRefusesWhatItCannotRead(t *testing.T) {
	config := secretsConfig(t)

	_, err := runApp(t, "init", "--config", config)
	require.ErrorContains(t, err, "one bundle")
	_, err = runApp(t, "init", "--config", filepath.Join(t.TempDir(), "absent.yaml"), "-")
	require.Error(t, err)
	_, err = runApp(t, "init", "--config", config, filepath.Join(t.TempDir(), "absent"))
	require.ErrorContains(t, err, "reading the bundle")

	app := newApp(nil)
	app.Reader = bytes.NewReader([]byte("not a bundle"))
	err = app.RunContext(t.Context(), []string{"taud", "init", "--config", config, "-"})
	require.ErrorContains(t, err, "reading the bundle")
}
