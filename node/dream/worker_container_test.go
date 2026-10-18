//go:build dreaming && docker_integration

package dream_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/specs"
)

// worker_container_test runs projects/with/worker/container: a `worker` with
// `runtime: container`, compiled from yaml, published, and invoked the way a cron
// or a grant invokes one — through the worker service, with a payload.
//
// THE CONTRACT IS pkg/invoke/container/job.go's: the payload is the run's stdin,
// exit 0 is an answer whose body is stdout, a non-zero exit is a refusal that
// says the code and what the run wrote to stderr. The image is busybox, whose
// default command is sh, so the payload is the script the container runs — and
// every byte of an answer below was computed inside it.
//
// It needs a docker daemon and CAP_NET_ADMIN, for application_test's reason:
// a run is a stranger's code, and the tier will not start one it cannot
// firewall. `make test-application` runs it.
func TestAContainerWorkerRunsToCompletionPerInvocation(t *testing.T) {
	m, err := dream.New(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(
				node.Register, node.Worker, node.Stats,
			).Containers(node.BackendDocker)},
		},
	}))
	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	// NOTHING IS STAGED: the image is the artifact, named in the document.
	at := publish(t, pids[:1], deployed{project: "shop", name: "with/worker/container", host: "exporter.example.com"}, artifacts{})[0]
	exporter := specs.Address{Project: "shop", Environment: "prod", Kind: "worker", Name: "exporter"}

	// A RUN THAT SUCCEEDS answers with what it printed. The arithmetic and the
	// variable are evaluated by the container's shell, so the body could not
	// have come from anywhere else.
	var last string
	var answer string
	require.Eventually(t, func() bool {
		res, err := at.onCarrying(t.Context(), exporter, http.MethodPost, "/", `echo "rows=$((40+2)) run=${#TAU_INVOCATION_ID}"`)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		answer = string(res.Body)
		return res.Status == 200
	}, 3*time.Minute, time.Second, "the worker never ran: %s", &lastSaid{&last})
	require.Equal(t, "rows=42 run=32", strings.TrimSpace(answer), "the payload ran inside the container, and was told its invocation id")

	// A RUN THAT FAILS is refused with its exit code and its own explanation.
	_, err = at.onCarrying(t.Context(), exporter, http.MethodPost, "/", `echo "bucket receipts: access denied" >&2; exit 3`)
	require.ErrorIs(t, err, container.ErrJobFailed)
	require.ErrorContains(t, err, "exited 3")
	require.ErrorContains(t, err, "bucket receipts: access denied")

	// EVERY INVOCATION IS ITS OWN RUN: a file the last one wrote is not there.
	_, err = at.onCarrying(t.Context(), exporter, http.MethodPost, "/", `echo left > /tmp/mark`)
	require.NoError(t, err)
	res, err := at.onCarrying(t.Context(), exporter, http.MethodPost, "/", `test -e /tmp/mark && echo shared || echo fresh`)
	require.NoError(t, err)
	require.Equal(t, "fresh", strings.TrimSpace(string(res.Body)))

	// AND A RUN PAST ITS TIMEOUT (20s, from the yaml) is killed and says so.
	start := time.Now()
	_, err = at.onCarrying(t.Context(), exporter, http.MethodPost, "/", `sleep 600`)
	require.ErrorIs(t, err, container.ErrJobTimedOut)
	require.Less(t, time.Since(start), 90*time.Second)
}
