// Package e2e runs taucorder against a real v2 fleet: node/dream nodes
// running register, discovery and worker, joined from outside the universe
// with nothing but a certificate from its CA and a bootstrap address.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/tools/taucorder/internal/cli"
	"github.com/taubyte/tau/tools/taucorder/internal/query"
)

func TestBridgesADreamFleet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	u, err := nodeDream.Start(ctx, "taucorder-e2e", nodeDream.Config{
		Services: []string{node.Register, node.Discovery, node.Worker},
		Copies:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	pids := u.Pids()
	n, ok := nodeDream.Node(pids[0])
	if !ok || n.Register() == nil {
		t.Fatal("first node runs no register")
	}
	if _, err := n.Register().Local().Publish(ctx, pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}

	// From outside: a key, a certificate from the universe's CA, and one
	// bootstrap address.
	dir := t.TempDir()
	private, public, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := crypto.MarshalPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := u.CA().Issue(public, u.Name(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"private-key": []byte(base64.StdEncoding.EncodeToString(raw)),
		"ca":          u.CA().CertificatePEM(),
		"certificate": cert,
	}
	first := u.Peers()[0]
	boot := first.Peer().Addrs()[0].String() + "/p2p/" + first.ID().String()
	base := []string{"taucorder", "--bootstrap", boot, "--cloud", u.Name()}
	for flag, content := range files {
		path := filepath.Join(dir, flag)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		base = append(base, "--"+flag, path)
	}

	addrs := make(chan string, 1)
	env := cli.Default()
	env.Out = &bytes.Buffer{}
	env.Listening = func(a string) { addrs <- a }
	sctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- cli.App(env).RunContext(sctx, append(base, "serve", "--listen", "127.0.0.1:0"))
	}()
	var addr string
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatal(err)
	}

	// A generated client against the local listener reaches the fleet's
	// register.
	r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, "http://"+addr)
	var envs []string
	deadline := time.Now().Add(90 * time.Second)
	for {
		got, err := r.Environments(ctx, connect.NewRequest(&registerv2.ProjectRequest{Project: "shop"}))
		if err == nil {
			envs = got.Msg.GetEnvironments()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never forwarded:", err)
		}
		time.Sleep(time.Second)
	}
	if !slices.Equal(envs, []string{"prod"}) {
		t.Fatal(envs)
	}
	// Read-only by default: a write never leaves the listener.
	_, err = r.Publish(ctx, connect.NewRequest(&registerv2.PublishRequest{Project: "shop"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("write was not refused:", err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// The fleet, listed from the discovery gossip.
	var out bytes.Buffer
	env = cli.Default()
	env.Out = &out
	if err := cli.App(env).RunContext(ctx, append(base, "nodes", "-o", "json", "--wait", "12s")); err != nil {
		t.Fatal(err)
	}
	var nodes []query.Node
	if err := json.Unmarshal(out.Bytes(), &nodes); err != nil {
		t.Fatal(err, out.String())
	}
	var seen []string
	for _, n := range nodes {
		seen = append(seen, n.Peer)
	}
	for _, pid := range pids {
		if !slices.Contains(seen, pid) {
			t.Fatalf("node %s missing from %v", pid, seen)
		}
	}

	out.Reset()
	if err := cli.App(env).RunContext(ctx, append(base, "services", "-o", "json", "--wait", "12s")); err != nil {
		t.Fatal(err)
	}
	var svcs []query.Service
	if err := json.Unmarshal(out.Bytes(), &svcs); err != nil {
		t.Fatal(err, out.String())
	}
	for _, want := range []string{node.Register, node.Worker, node.Discovery} {
		if !slices.ContainsFunc(svcs, func(s query.Service) bool { return s.Name == want && len(s.Nodes) == 2 }) {
			t.Fatalf("service %s not on both nodes: %+v", want, svcs)
		}
	}
}
