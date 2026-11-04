//go:build docker_integration

package live_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gerrit"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/git/repokey"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/ingress"
	"github.com/taubyte/tau/services/build/ingress/forgeauth"
)

func TestARealGerritStreamsItsPushes(t *testing.T) {
	ctx := t.Context()
	httpPort, sshPort := freePort(t), freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", httpPort)
	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	base := "http://" + host

	start(t, &core.ContainerConfig{
		Image: gerritImage,
		Env:   []string{"CANONICAL_WEB_URL=" + base + "/"},
		Network: &core.NetworkConfig{Mode: "bridge", PortMappings: []core.PortMapping{
			{HostPort: httpPort, ContainerPort: 8080, HostIP: "127.0.0.1"},
			{HostPort: sshPort, ContainerPort: 29418, HostIP: "127.0.0.1"},
		}},
	})
	eventually(t, "gerrit ready", 4*time.Minute, func() bool {
		resp, err := http.Get(base + "/config/server/version")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	asAdmin := http.Header{}
	asAdmin.Set("Authorization", "Basic "+basic("admin", "secret"))
	// Administrators is a group with a UUID, not a system group; Registered
	// Users is, and the administrator is one.
	admins := map[string]any{"global:Registered-Users": map[string]string{"action": "ALLOW"}}
	api(t, http.MethodPost, base+"/a/projects/All-Projects/access", asAdmin, map[string]any{"add": map[string]any{
		"refs/*": map[string]any{"permissions": map[string]any{
			"forgeAuthor":    map[string]any{"rules": admins},
			"forgeCommitter": map[string]any{"rules": admins},
		}},
		"refs/heads/*": map[string]any{"permissions": map[string]any{
			"push":   map[string]any{"rules": admins},
			"create": map[string]any{"rules": admins},
		}},
	}}, nil)

	// THE DEPLOYMENT'S SSH ACCOUNT is the administrator, which holds Stream
	// Events. Its key is registered over REST as plain text.
	priv, pub := gittest.KeyPair(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/a/accounts/self/sshkeys", strings.NewReader(string(gossh.MarshalAuthorizedKey(pub))))
	require.NoError(t, err)
	req.Header = asAdmin.Clone()
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "registering the stream account's key")

	// The harness learns the sshd's host key the way ssh-keyscan would; the
	// driver is then pinned to it, and only to it.
	hostKey := scanHostKey(t, sshAddr)

	project := fmt.Sprintf("acme/stream-%d", time.Now().UnixNano())
	api(t, http.MethodPut, base+"/a/projects/"+strings.ReplaceAll(project, "/", "%2F"), asAdmin, map[string]any{}, nil)

	drv, err := gerrit.New(host, gerrit.Plaintext(), gerrit.Membership("admin:secret"), gerrit.Events(sshAddr, "admin", []byte(priv), hostKey))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowInsecureCallback: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	require.Equal(t, []string{host}, reg.EventHosts())
	key, err := repokey.Parse("http://" + host + "/" + project)
	require.NoError(t, err)
	remote := base + "/a/" + project
	auth := &githttp.BasicAuth{Username: "admin", Password: "secret"}

	t.Log("a real push arrives on the stream")
	pushes := make(chan forge.Push, 64)
	sub, stop := context.WithCancel(ctx)
	ready := make(chan struct{})
	streamed := make(chan error, 1)
	go func() {
		streamed <- reg.StreamPushes(sub, host, func() { close(ready) }, func(p forge.Push, err error) {
			if err == nil {
				pushes <- p
			}
		})
	}()
	select {
	case <-ready:
	case err := <-streamed:
		t.Fatalf("the stream was refused: %v", err)
	case <-time.After(time.Minute):
		t.Fatal("the stream was not accepted")
	}
	// GERRIT REGISTERS A LISTENER AFTER IT ACCEPTS THE COMMAND, so the first
	// moments of a subscription may hear nothing. Probing with throwaway
	// projects until one is heard is the barrier — each creates a ref, and
	// the stream is known live once any of them arrives.
	probe := 0
	eventually(t, "the stream hears its first event", 2*time.Minute, func() bool {
		probe++
		api(t, http.MethodPut, fmt.Sprintf("%s/a/projects/acme%%2Fprobe-%d-%d", base, time.Now().UnixNano(), probe), asAdmin, map[string]any{"create_empty_commit": true}, nil)
		select {
		case <-pushes:
			return true
		case <-time.After(time.Second):
			return false
		}
	})
	drain(pushes)

	first := pushFiles(t, remote, auth, "refs/heads/main", map[string]string{"README": "first"})
	got := awaitPush(t, pushes, key, "refs/heads/main")
	require.Equal(t, forge.Push{Key: key, Ref: "refs/heads/main", Commit: first}, got)

	t.Log("disconnect, and a push nobody hears")
	stop()
	require.ErrorIs(t, <-streamed, context.Canceled)
	missed := pushFiles(t, remote, auth, "refs/heads/master", map[string]string{"README": "missed"})

	heads, err := reg.Heads(ctx, key)
	require.NoError(t, err)
	require.ElementsMatch(t, []forge.Push{
		{Key: key, Ref: "refs/heads/main", Commit: first},
		{Key: key, Ref: "refs/heads/master", Commit: missed},
	}, heads, "the heads are what a reconnect compares")

	t.Log("the frontend's reconnect finds the missed push and enqueues it")
	forges, err := forgeauth.New(reg)
	require.NoError(t, err)
	backend := &recordingBackend{}
	svc, err := ingress.New(ingress.Config{Listen: "127.0.0.1:0", StreamResync: time.Hour},
		ingress.WithHooks(noHooks{}), ingress.WithDeliveries(forges), ingress.WithProjects(noProjects{}),
		ingress.WithBackend(backend), ingress.WithCallers(noCallers{}),
		ingress.WithStreams(forges, linkedRepos{key.String()}),
		ingress.WithLogger(t.Logf))
	require.NoError(t, err)
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() { svc.Close() })
	eventually(t, "the missed push enqueued", time.Minute, func() bool {
		return backend.has(key.String(), "refs/heads/master", missed)
	})
	require.True(t, backend.has(key.String(), "refs/heads/main", first), "the head already built is enqueued again, which the backend's job id makes the same job")
}

// scanHostKey is the key the sshd at addr answers with.
func scanHostKey(t *testing.T, addr string) gossh.PublicKey {
	t.Helper()
	var seen gossh.PublicKey
	var conn net.Conn
	eventually(t, "gerrit sshd up", 2*time.Minute, func() bool {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return false
		}
		conn = c
		return true
	})
	defer conn.Close()
	gossh.NewClientConn(conn, addr, &gossh.ClientConfig{
		User: "scan",
		HostKeyCallback: func(_ string, _ net.Addr, k gossh.PublicKey) error {
			seen = k
			return errors.New("scanned")
		},
	})
	require.NotNil(t, seen, "the sshd offered no host key")
	return seen
}

// pushFiles puts a fresh commit holding files on ref at remote.
func pushFiles(t *testing.T, remote string, auth *githttp.BasicAuth, ref string, files map[string]string) string {
	t.Helper()
	st, commit := gittest.Repository(t, files)
	repo, err := gogit.Open(st.(storage.Storer), nil)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{remote}})
	require.NoError(t, err)
	require.NoError(t, repo.PushContext(t.Context(), &gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec("refs/heads/main:" + ref)},
		Auth:       auth,
	}))
	return commit
}

func awaitPush(t *testing.T, pushes <-chan forge.Push, key repokey.Key, ref string) forge.Push {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case p := <-pushes:
			if p.Key == key && p.Ref == ref {
				return p
			}
		case <-deadline:
			t.Fatalf("no push to %s %s arrived on the stream", key, ref)
		}
	}
}

func drain(c chan forge.Push) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

// The frontend's other dependencies, which a stream never asks.
type noHooks struct{}

func (noHooks) Hook(context.Context, string) (ingress.Hook, error) {
	return ingress.Hook{}, ingress.ErrNoHook
}

type noProjects struct{}

func (noProjects) Role(context.Context, string, string) (register.Role, error) {
	return "", errors.New("unused")
}

func (noProjects) Project(context.Context, string) (string, error) { return "", errors.New("unused") }

type linkedRepos []string

func (l linkedRepos) LinkedRepositories(context.Context) ([]string, error) { return l, nil }

type recordingBackend struct {
	mu   sync.Mutex
	sent []string
}

func (b *recordingBackend) Enqueue(_ context.Context, repo, ref, sha string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, repo+" "+ref+" "+sha)
	return "job", nil
}

func (b *recordingBackend) has(repo, ref, sha string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.sent {
		if s == repo+" "+ref+" "+sha {
			return true
		}
	}
	return false
}

func (b *recordingBackend) Get(context.Context, string) (*buildv2.Job, error) {
	return nil, errors.New("unused")
}
func (b *recordingBackend) List(context.Context, string, func(string) error) error {
	return errors.New("unused")
}
func (b *recordingBackend) Cancel(context.Context, string) error { return errors.New("unused") }
func (b *recordingBackend) Retry(context.Context, string) error  { return errors.New("unused") }
func (b *recordingBackend) Follow(context.Context, string, int32, int64, func(*buildv2.Job_Event) error) (int64, error) {
	return 0, errors.New("unused")
}
func (b *recordingBackend) Snapshot(context.Context, string, int32, int64, func(*buildv2.Job_Event) error) (int64, error) {
	return 0, errors.New("unused")
}

// noCallers admits nobody: this test makes no caller's request.
type noCallers struct{}

func (noCallers) Caller(context.Context, string) (string, error) {
	return "", ingress.ErrBadCredential
}
