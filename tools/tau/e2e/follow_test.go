//go:build dreaming

package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/tools/tau/internal/cli"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// heldEngine says one line, then holds the build until released, then fails
// it: a build in the middle of its work, and one whose output matters.
type heldEngine struct {
	release chan struct{}
}

func (e *heldEngine) Make(ctx context.Context, spec artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	io.WriteString(log, "compiling the held build\n")
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	io.WriteString(log, "error: the linker gave up\n")
	return nil, errors.New("exit-code:1")
}

// watched is a writer that says when it first holds want.
type watched struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	want string
	seen chan struct{}
}

func (w *watched) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	had := strings.Contains(w.buf.String(), w.want)
	w.buf.Write(p)
	if !had && strings.Contains(w.buf.String(), w.want) {
		close(w.seen)
	}
	return len(p), nil
}

func (w *watched) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// A BUILD IS WATCHED WHILE IT RUNS. `tau ci run` prints the build's first line
// while the build is still holding, which is the job's live log followed from
// the node running it to the CLI; the failure's own output then follows, and
// `tau logs` reads all of it back once the job has ended.
func TestTauCIRunFollowsALiveBuild(t *testing.T) {
	engine := &heldEngine{release: make(chan struct{})}
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	gitEnv(t)

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	cloud, _ := fleet(t, f, node.WithBuildSeams(func(s node.BuildSeams) node.BuildSeams {
		s.Engine = engine
		return s
	}))
	var login result
	require.Eventually(t, func() bool {
		login = tauIn(f.TokenString()+"\n", "login", "--cloud", cloud, "--sso", f.Host(), "--paste")
		return login.code == 0
	}, 2*time.Minute, time.Second, "login never succeeded: %s", login.stderr)

	work := t.TempDir()
	require.NoError(t, copyProject(filepath.Join("..", "..", "..", "node", "dream", "projects", "with", "function"), work))
	configRepo := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "follow"}
	var link result
	require.Eventually(t, func() bool {
		link = tau("link", "--create", "--push", "--dir", work, "--config-repo", configRepo.String(), "-o", "json")
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)

	live := &watched{want: "compiling the held build", seen: make(chan struct{})}
	var out bytes.Buffer
	exited := make(chan int, 1)
	go func() {
		exited <- cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: live, HTTP: http.DefaultClient, Forges: forges},
			[]string{"ci", "run", "--dir", work, "--interval", "200ms"})
	}()

	select {
	case <-live.seen:
	case code := <-exited:
		t.Fatalf("tau ci run ended (%d) before the build said anything: %s", code, live.String())
	case <-time.After(3 * time.Minute):
		close(engine.release)
		t.Fatalf("the build's first line never reached tau ci run while it ran: %s", live.String())
	}
	select {
	case code := <-exited:
		t.Fatalf("tau ci run ended (%d) while the build was held", code)
	default:
	}

	close(engine.release)
	select {
	case code := <-exited:
		require.Equal(t, 1, code, "a failed build fails tau ci run: %s", live.String())
	case <-time.After(2 * time.Minute):
		t.Fatal("tau ci run never ended after the build did")
	}
	require.Contains(t, live.String(), "error: the linker gave up", "the failure's output is followed too")

	_, rest, found := strings.Cut(live.String(), "queued job ")
	require.True(t, found, "tau ci run names its job: %s", live.String())
	job, _, _ := strings.Cut(rest, ":")
	require.Contains(t, out.String(), "job "+job+": failed")
	stored := ok(t, "logs", "--", job)
	require.Contains(t, stored, "compiling the held build")
	require.Contains(t, stored, "error: the linker gave up")
}
