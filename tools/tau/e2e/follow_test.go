//go:build dreaming && docker_integration

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/tools/tau/internal/cli"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// held is a package whose build says one line and then holds, a build in the
// middle of its work, until somebody cancels it.
const held = `id: QmXoj8UEVte5oUsVvmYkSa6NQ7tkfDsyPRP97HRckJQ1jV
type: wasm
source: services/hello
output: handler.wasm
build:
    steps:
        - name: build
          image: busybox:1.36
          commands:
              - echo compiling the held build
              - sleep 3600
`

// failing is the same package built by a recipe that fails at once, with output
// that matters.
const failing = `id: QmXoj8UEVte5oUsVvmYkSa6NQ7tkfDsyPRP97HRckJQ1jV
type: wasm
source: services/hello
output: handler.wasm
build:
    steps:
        - name: build
          image: busybox:1.36
          commands:
              - echo compiling the queued build
              - "echo error: the linker gave up"
              - exit 1
`

// recipe writes what the project's one package is built by.
func recipe(t *testing.T, work, yaml string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(work, "package", "hello_code.yaml"), []byte(yaml), 0o644))
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
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	gitEnv(t)

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	cloud, _ := universe(t, f, nil)
	var login result
	require.Eventually(t, func() bool {
		login = tauIn(f.TokenString()+"\n", "login", "--cloud", cloud, "--sso", f.Host(), "--paste")
		return login.code == 0
	}, 2*time.Minute, time.Second, "login never succeeded: %s", login.stderr)

	work := t.TempDir()
	require.NoError(t, copyProject(filepath.Join("..", "..", "..", "instance", "dream", "projects", "with", "function", "wasm"), work))
	require.NoError(t, os.MkdirAll(filepath.Join(work, "services", "hello"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "services", "hello", "README"), []byte("built by its recipe\n"), 0o644))
	recipe(t, work, held)
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
	}
	select {
	case code := <-exited:
		t.Fatalf("tau ci run ended (%d) while the build was held", code)
	default:
	}

	// A PUSH IS A PLAN, and the build is the environment job it queued, which tau ci
	// run names as it follows it.
	plan := named(t, live.String(), "queued job ", ":")
	job := named(t, live.String(), "building as job ", "\n")
	// RELEASED BY A CANCEL, the one way a held build ends before its time.
	release := sync.OnceFunc(func() { ok(t, "cancel", job, "--yes") })
	t.Cleanup(release)

	soFar := tau("logs", "--", job)
	require.Equal(t, 0, soFar.code, soFar.stderr)
	require.Contains(t, soFar.stdout, "compiling the held build")
	require.NotContains(t, soFar.stdout, "context canceled", "and it did not wait for the held build to end")
	require.Contains(t, tau("logs", "--", plan).stderr, "built as "+job, "the plan names the job it queued")

	followed := &watched{want: "compiling the held build", seen: make(chan struct{})}
	following := make(chan int, 1)
	go func() {
		following <- cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: followed, Stderr: io.Discard, HTTP: http.DefaultClient, Forges: forges},
			[]string{"logs", "--follow", "--", job})
	}()
	select {
	case <-followed.seen:
	case code := <-following:
		t.Fatalf("tau logs --follow ended (%d) while the build was held: %s", code, followed.String())
	}
	select {
	case code := <-following:
		t.Fatalf("tau logs --follow ended (%d) while the build was held", code)
	default:
	}

	queued := queueBehind(t, f, work, configRepo, failing)
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
		t.Fatalf("tau logs --follow of a queued job ended (%d) without saying it was queued: %s", code, note.String())
	}
	require.Contains(t, ok(t, "job", "-o", "json", "--", queued), `"status": "open"`, "still behind the held build")
	select {
	case code := <-waitingFollow:
		t.Fatalf("tau logs --follow of a queued job ended (%d) before it started: %s", code, waited.String())
	default:
	}

	release()
	require.Equal(t, 1, <-exited, "a cancelled build fails tau ci run: %s", live.String())
	require.Contains(t, live.String(), "context canceled", "the end of its output is followed too")
	require.Equal(t, 0, <-following)
	require.Contains(t, followed.String(), "context canceled", "the rest arrives while it follows")

	require.Contains(t, out.String(), "job "+plan+": cancelled")
	stored := ok(t, "logs", "--", job)
	require.Contains(t, stored, "compiling the held build")
	require.Contains(t, stored, "context canceled")
	require.Equal(t, stored, ok(t, "logs", "--follow", "--", job), "an ended job's log is the stored one either way")

	require.Equal(t, 0, <-waitingFollow)
	require.Contains(t, waited.String(), "compiling the queued build")
	require.Contains(t, waited.String(), "error: the linker gave up")
	behind := named(t, note.String(), "building as job ", "\n")
	require.Equal(t, "main feeds prod\nqueued job "+behind+" for prod\n"+ok(t, "logs", "--", behind), waited.String(),
		"the follow that waited got what the plan said, then the whole log of the job it queued")
}

// named is the id that follows prefix in what a command said, up to end.
func named(t *testing.T, said, prefix, end string) string {
	t.Helper()
	_, rest, found := strings.Cut(said, prefix)
	require.True(t, found, "%q is not in: %s", prefix, said)
	id, _, _ := strings.Cut(rest, end)
	require.NotEmpty(t, id)
	return id
}

// queueBehind pushes a second commit to the configuration repository, its package
// built by the recipe given, and has tau ci run queue its build, which waits for
// the fleet's one build backend.
func queueBehind(t *testing.T, f *fake.Served, work string, repo repokey.Key, yaml string) string {
	t.Helper()
	s, err := forges.For(repo, f.Token())
	require.NoError(t, err)
	addr, err := s.CloneAddress(repo, forge.HTTPS)
	require.NoError(t, err)
	recipe(t, work, yaml)
	gitOK(t, work, "commit", "-q", "-am", "queued behind the held build")
	gitOK(t, work, "-c", "http.extraHeader=Authorization: Basic "+basic(addr.HTTPUsername, f.TokenString()), "push", "-q", addr.URL, "HEAD:refs/heads/main")

	type queuedJob struct {
		Job string `json:"job"`
	}
	var queued queuedJob
	require.NoError(t, json.Unmarshal([]byte(ok(t, "ci", "run", "--detach", "--dir", work, "-o", "json")), &queued))
	require.NotEmpty(t, queued.Job)
	return queued.Job
}
