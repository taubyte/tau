package ssh_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"

	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/instance/boot"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/secrets"
	sshSrv "github.com/taubyte/tau/tools/spore-drive/clients/mock/ssh"
	driveSrv "github.com/taubyte/tau/tools/spore-drive/drive/service"
	drivepb "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2"
	"github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2/drivev2connect"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1"
	"github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1/mockv1connect"
)

// mock serves the drive and the mock ssh hosts the way the client tests'
// mock binary does, in process.
func mock(t *testing.T) (drivev2connect.DriveServiceClient, mockv1connect.MockSSHServiceClient) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	d, err := driveSrv.Serve(ctx)
	assert.NilError(t, err)
	s, err := sshSrv.Serve(ctx)
	assert.NilError(t, err)

	mux := http.NewServeMux()
	d.Attach(mux)
	s.Attach(mux)
	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(srv.Close)
	return drivev2connect.NewDriveServiceClient(srv.Client(), srv.URL), mockv1connect.NewMockSSHServiceClient(srv.Client(), srv.URL)
}

// files reads a host's disk back out of the mock.
func files(t *testing.T, m mockv1connect.MockSSHServiceClient, host string) map[string][]byte {
	t.Helper()
	stream, err := m.Filesystem(context.Background(), connect.NewRequest(&pb.Host{Name: host}))
	assert.NilError(t, err)
	var bundle bytes.Buffer
	for stream.Receive() {
		bundle.Write(stream.Msg().GetData())
	}
	assert.NilError(t, stream.Err())

	out := map[string][]byte{}
	tr := tar.NewReader(&bundle)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		assert.NilError(t, err)
		data, err := io.ReadAll(tr)
		assert.NilError(t, err)
		out[h.Name] = data
	}
}

func TestADeployWritesTheNodeFileItPlanned(t *testing.T) {
	d, m := mock(t)
	ctx := context.Background()

	var ports []int32
	for _, name := range []string{"h1", "h2"} {
		h, err := m.New(ctx, connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: name}, AuthUsername: "tau", AuthPassword: "testtest"}))
		assert.NilError(t, err)
		ports = append(ports, h.Msg.GetPort())
	}

	doc := &drivepb.Document{Files: map[string][]byte{
		"cloud.yaml": []byte("domain:\n  root: test.com\n  generated: g.test.com\np2p:\n  bootstrap:\n    all: [h1, h2]\n" +
			"auth:\n  forges:\n    - {host: github.com, driver: github, clientid: gh-id, clientsecret: gh-s3cret}\n"),
		"shapes.yaml": []byte("all:\n  services: [auth, seer, gateway]\n  ports:\n    main: 4242\n"),
		"hosts.yaml": []byte(fmt.Sprintf("h1:\n  addr: [127.0.0.1/32]\n  ssh: {addr: 127.0.0.1, port: %d, auth: [main]}\n  shapes: {all: {}}\n"+
			"h2:\n  addr: [127.0.0.2/32]\n  ssh: {addr: 127.0.0.1, port: %d, auth: [main]}\n  shapes: {all: {}}\n", ports[0], ports[1])),
		"auth.yaml":    []byte("main:\n  username: tau\n  password: testtest\n"),
		"added.yaml":   []byte("example:\n  greeting: hello\n  replicas: 2\n"),
		"tenancy.yaml": []byte("provider: github\nowner: Acme\n"),
	}}
	v, err := d.Validate(ctx, connect.NewRequest(&drivepb.ValidateRequest{Document: doc}))
	assert.NilError(t, err)
	assert.Equal(t, len(v.Msg.GetErrors()), 0, v.Msg.GetErrors())

	tau := filepath.Join(t.TempDir(), "tau")
	assert.NilError(t, os.WriteFile(tau, []byte("#!/bin/sh\n"), 0755))
	course, err := d.Plan(ctx, connect.NewRequest(&drivepb.PlanRequest{Document: v.Msg.GetDocument(), Tau: &drivepb.PlanRequest_Path{Path: tau}}))
	assert.NilError(t, err)

	progress, err := d.Apply(ctx, connect.NewRequest(&drivepb.ApplyRequest{Course: course.Msg.GetId()}))
	assert.NilError(t, err)
	for progress.Receive() {
		assert.Equal(t, progress.Msg().GetError(), "", progress.Msg().GetStep())
	}
	assert.NilError(t, progress.Err())

	var keys []string
	var stdins [][]byte
	for _, host := range []string{"h1", "h2"} {
		disk := files(t, m, host)
		raw, ok := disk["/tb/config/all.yaml"]
		assert.Assert(t, ok, "the node file was installed on %s; its disk holds %d files", host, len(disk))

		var f boot.File
		assert.NilError(t, yaml.Unmarshal(raw, &f))
		assert.DeepEqual(t, f.Node.Services, []string{"auth", "secrets", "stats", "dns", "gateway"})
		assert.Equal(t, f.Node.Cloud, "test.com")
		assert.Equal(t, f.Node.CloudFqdn, "g.test.com")
		assert.DeepEqual(t, f.Node.Tenancy, []string{"github.com/acme"})
		assert.DeepEqual(t, f.Node.Sections, map[string]any{"example": map[string]any{"greeting": "hello", "replicas": 2}})
		assert.Assert(t, f.CA != "" && f.Certificate != "" && f.PrivateKey != "")
		assert.DeepEqual(t, f.Listen, []string{"/ip4/0.0.0.0/tcp/4242"})
		assert.Equal(t, len(f.Bootstrap), 1, "a host bootstraps from the others")
		keys = append(keys, f.PrivateKey)

		// THE SECRET REACHED THE NODE'S INIT ON STDIN, and no file holds it.
		for name, data := range disk {
			assert.Assert(t, !bytes.Contains(data, []byte("gh-s3cret")), "%s on %s holds the secret", name, host)
		}
		cmds, err := m.Commands(ctx, connect.NewRequest(&pb.Host{Name: host}))
		assert.NilError(t, err)
		var inits []string
		for cmds.Receive() {
			c := cmds.Msg()
			if strings.Contains(c.GetCommand(), `"init"`) {
				inits = append(inits, c.GetCommand())
				stdins = append(stdins, c.GetStdin())
				continue
			}
			assert.Equal(t, len(c.GetStdin()), 0, "%q was handed stdin", c.GetCommand())
		}
		assert.NilError(t, cmds.Err())
		assert.DeepEqual(t, inits, []string{`sudo "/tb/bin/tau" "init" "--config" "/tb/config/all.yaml" "--root" "/tb/storage/all" "-"`})
		assert.Assert(t, !strings.Contains(strings.Join(inits, ""), "gh-s3cret"))
	}
	assert.Assert(t, keys[0] != keys[1], "each host has its own identity")

	// One sealed bundle, byte for byte the same on every host, holding the
	// secret sealed rather than in the clear.
	assert.Equal(t, len(stdins), 2)
	assert.DeepEqual(t, stdins[0], stdins[1])
	assert.Assert(t, !bytes.Contains(stdins[0], []byte("gh-s3cret")))
	var bundle secretsv2.InitBundle
	assert.NilError(t, proto.Unmarshal(stdins[0], &bundle))
	opened := map[string]string{}
	for _, sealed := range bundle.GetSecrets() {
		ref, value, err := secrets.Unsealed(sealed)
		assert.NilError(t, err)
		opened[ref.String()] = string(value)
	}
	ref := secrets.Ref{Scope: secrets.Cloud(authService.VaultService), Name: authService.OAuthSecretName("github.com")}
	assert.Equal(t, opened[ref.String()], "gh-s3cret")

	stream, err := m.Filesystem(ctx, connect.NewRequest(&pb.Host{Name: "nobody"}))
	assert.NilError(t, err)
	assert.Assert(t, !stream.Receive())
	assert.Equal(t, connect.CodeOf(stream.Err()), connect.CodeNotFound)
}
