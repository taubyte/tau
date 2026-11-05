//go:build dreaming && docker_integration

package dream_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/containers"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/specs"
)

func TestAContainerWorkerRunsToCompletionPerInvocation(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{instance.Register, instance.Worker, instance.Discovery, storageService.Descriptor.Name, instance.Runner},
		Container: containers.Docker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	require.NotEmpty(t, pids)

	// NOTHING IS STAGED: the image is the artifact, named in the document.
	at := publish(t, u, pids[:1], deployed{project: "shop", name: "with/worker/container", host: "exporter.example.com"}, artifacts{})[0]
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

func TestAContainerWorkerReadsItsSecretThroughTheHelper(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{instance.Register, instance.Worker, instance.Discovery, instance.Secrets, storageService.Descriptor.Name, instance.Runner},
		Container: containers.Docker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	require.NotEmpty(t, pids)

	at := publish(t, u, pids[:1], deployed{project: "shop", name: "with/worker/vault", host: "exporter.example.com"}, artifacts{})[0]
	exporter := specs.Address{Project: "shop", Environment: "prod", Kind: "worker", Name: "exporter"}
	token := at.idOf(t, "secret", "token")

	// SET THE WAY A DEVELOPER SETS ONE; the first run retries until it lands.
	ctx := t.Context()
	require.NoError(t, at.node.Vault().Set(ctx, "shop", "prod", token, []byte("sk_live_1")))

	run := func(script string) (string, error) {
		res, err := at.onCarrying(ctx, exporter, http.MethodPost, "/", script)
		if err != nil {
			return "", err
		}
		return string(res.Body), nil
	}

	var last string
	var answer string
	require.Eventually(t, func() bool {
		answer, err = run(`/.taubyte/secret token`)
		if err != nil {
			last = err.Error()
			return false
		}
		return true
	}, 3*time.Minute, time.Second, "the worker never read its secret: %s", &lastSaid{&last})
	require.Equal(t, "sk_live_1", answer, "the value as stored, read when the run asked")

	said, err := run(`env | grep -q sk_live_1 && echo leaked || echo clean`)
	require.NoError(t, err)
	require.Equal(t, "clean", strings.TrimSpace(said), "never in the environment")

	// A RUN THAT FAILS SAYING ITS SECRET has it masked in the failure it is refused with.
	_, err = run(`echo "rejected key $(/.taubyte/secret token)" >&2; exit 3`)
	require.ErrorIs(t, err, container.ErrJobFailed)
	require.ErrorContains(t, err, "rejected key ***")
	require.NotContains(t, err.Error(), "sk_live_1")

	// WHAT IT WAS NOT GRANTED is refused, with the vault's reason.
	_, err = run(`/.taubyte/secret dbcreds`)
	require.ErrorIs(t, err, container.ErrJobFailed)
	require.ErrorContains(t, err, `names no secret "dbcreds"`)

	// A ROTATION is what the next run reads; nothing restarts.
	require.NoError(t, at.node.Vault().Set(ctx, "shop", "prod", token, []byte("sk_live_2")))
	require.Eventually(t, func() bool {
		answer, err = run(`/.taubyte/secret token`)
		return err == nil && answer == "sk_live_2"
	}, time.Minute, time.Second, "the rotated value was never read")
}
