//go:build dreaming

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/taubyte/tau/pkg/forge"
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
	require.NoError(t, copyProject(filepath.Join("..", "..", "..", "node", "dream", "projects", "with", "function", "wasm"), work))
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

	_, rest, found := strings.Cut(live.String(), "queued job ")
	require.True(t, found, "tau ci run names its job: %s", live.String())
	job, _, _ := strings.Cut(rest, ":")

	soFar := make(chan result, 1)
	go func() { soFar <- tau("logs", "--", job) }()
	select {
	case r := <-soFar:
		require.Equal(t, 0, r.code, r.stderr)
		require.Contains(t, r.stdout, "compiling the held build")
		require.NotContains(t, r.stdout, "error: the linker gave up")
	case <-time.After(time.Minute):
		close(engine.release)
		t.Fatal("tau logs without --follow waited for the held build to end")
	}

	followed := &watched{want: "compiling the held build", seen: make(chan struct{})}
	following := make(chan int, 1)
	go func() {
		following <- cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: followed, Stderr: io.Discard, HTTP: http.DefaultClient, Forges: forges},
			[]string{"logs", "--follow", "--", job})
	}()
	select {
	case <-followed.seen:
	case code := <-following:
		close(engine.release)
		t.Fatalf("tau logs --follow ended (%d) while the build was held: %s", code, followed.String())
	case <-time.After(time.Minute):
		close(engine.release)
		t.Fatalf("tau logs --follow never printed the held build's first line: %s", followed.String())
	}
	select {
	case code := <-following:
		t.Fatalf("tau logs --follow ended (%d) while the build was held", code)
	default:
	}

	queued := queueBehind(t, f, work, configRepo)
	require.Contains(t, ok(t, "job", "-o", "json", "--", queued), `"status": "open"`)
	waiting := tau("logs", "--", queued)
	require.Equal(t, 0, waiting.code, waiting.stderr)
	require.Empty(t, waiting.stdout)
	require.Contains(t, waiting.stderr, "job "+queued+" is queued")

	waited := &watched{want: "error: the linker gave up", seen: make(chan struct{})}
	note := &watched{want: "job " + queued + " is queued", seen: make(chan struct{})}
	waitingFollow := make(chan int, 1)
	go func() {
		waitingFollow <- cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: waited, Stderr: note, HTTP: http.DefaultClient, Forges: forges},
			[]string{"logs", "--follow", "--", queued})
	}()
	select {
	case <-note.seen:
	case code := <-waitingFollow:
		close(engine.release)
		t.Fatalf("tau logs --follow of a queued job ended (%d) without saying it was queued: %s", code, note.String())
	case <-time.After(time.Minute):
		close(engine.release)
		t.Fatalf("tau logs --follow of a queued job never said it was queued: %s", note.String())
	}
	require.Contains(t, ok(t, "job", "-o", "json", "--", queued), `"status": "open"`, "still behind the held build")
	select {
	case code := <-waitingFollow:
		t.Fatalf("tau logs --follow of a queued job ended (%d) before it started: %s", code, waited.String())
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
	select {
	case code := <-following:
		require.Equal(t, 0, code)
	case <-time.After(2 * time.Minute):
		t.Fatal("tau logs --follow never ended after the build did")
	}
	require.Contains(t, followed.String(), "error: the linker gave up", "the rest arrives while it follows")

	require.Contains(t, out.String(), "job "+job+": failed")
	stored := ok(t, "logs", "--", job)
	require.Contains(t, stored, "compiling the held build")
	require.Contains(t, stored, "error: the linker gave up")
	require.Equal(t, stored, ok(t, "logs", "--follow", "--", job), "an ended job's log is the stored one either way")

	select {
	case code := <-waitingFollow:
		require.Equal(t, 0, code)
	case <-time.After(2 * time.Minute):
		t.Fatal("tau logs --follow of the queued job never ended after it did")
	}
	require.Contains(t, waited.String(), "compiling the held build")
	// The follow ends with the log, which closes before the record settles.
	require.Eventually(t, func() bool {
		r := tau("job", "-o", "json", "--", queued)
		return r.code == 0 && strings.Contains(r.stdout, `"status": "failed"`)
	}, 2*time.Minute, time.Second, "the queued job never settled")
	require.Equal(t, ok(t, "logs", "--", queued), waited.String(), "the follow that waited got the whole log")
}

// queueBehind pushes a second commit to the configuration repository and has
// tau ci run queue its build, which waits for the fleet's one build backend.
func queueBehind(t *testing.T, f *fake.Served, work string, repo repokey.Key) string {
	t.Helper()
	s, err := forges.For(repo, f.Token())
	require.NoError(t, err)
	addr, err := s.CloneAddress(repo, forge.HTTPS)
	require.NoError(t, err)
	gitOK(t, work, "commit", "-q", "--allow-empty", "-m", "queued behind the held build")
	gitOK(t, work, "-c", "http.extraHeader=Authorization: Basic "+basic(addr.HTTPUsername, f.TokenString()), "push", "-q", addr.URL, "HEAD:refs/heads/main")

	var queued struct {
		Job string `json:"job"`
	}
	require.NoError(t, json.Unmarshal([]byte(ok(t, "ci", "run", "--detach", "--dir", work, "-o", "json")), &queued))
	require.NotEmpty(t, queued.Job)
	return queued.Job
}
