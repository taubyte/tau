//go:build dreaming && docker_integration

package dream_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/specs"
)

func TestAContainerWorkerRunsToCompletionPerInvocation(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{node.Register, node.Worker, node.Discovery},
		Container: node.BackendDocker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	require.NotEmpty(t, pids)

	// NOTHING IS STAGED: the image is the artifact, named in the document.
	at := publish(t, pids[:1], deployed{project: "shop", name: "with/worker/container", host: "exporter.example.com"}, artifacts{})[0]
	exporter := specs.Address{Project: "shop", Environment: "prod", Kind: "worker", Name: "exporter"}

	// A successful run answers with what it printed; the container's shell evaluates the
	// arithmetic and variable.
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
