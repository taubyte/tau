//go:build dreaming && docker_integration

package dream_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/specs"
)

// application_test is the first time a container this cloud started answers a
// request. Everything before it ran the container tier over a fake backend, or
// asked a real daemon to CREATE a container and stopped there.
//
// THE PATH IS THE AUTHOR'S, END TO END: projects/with/application is compiled by
// tcc, published to the register, routed by the gateway, placed onto a worker,
// and run by the same container tier a machine's config builds — over a real
// docker daemon, behind the egress firewall production installs.
//
// IT NEEDS WHAT PRODUCTION NEEDS, and fails rather than skips without it. The
// tier always restricts egress, and the docker backend refuses to create a
// container it cannot firewall (pkg/containers/backends/docker/docker.go), which
// takes CAP_NET_ADMIN. So it is run through `make test-application`, which builds
// this binary as the user and runs only the binary with that privilege. A test
// that skipped here would print `ok` having started nothing — the exact silence
// this file exists to end.
func TestAnApplicationAnswersThroughTheGateway(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{node.Register, node.Worker, node.Stats, node.Gateway},
		Container: node.BackendDocker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	require.NotEmpty(t, pids)

	// NOTHING IS STAGED: the image is the artifact, named in the document.
	at := publish(t, pids[:1], deployed{project: "shop", name: "with/application", host: "whoami.example.com"}, artifacts{})[0]

	whoami := specs.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "whoami"}

	// A REAL SOCKET IN FRONT OF THE GATEWAY, so the request is HTTP on the wire and
	// not a handler call.
	edge := httptest.NewServer(at.node.Handler())
	t.Cleanup(edge.Close)

	// A PATH NOTHING COULD ANSWER BUT THE CONTAINER. whoami writes the request line
	// it received into its body, so a body carrying this path came from inside the
	// container — a gateway, a fallback or a fake could not have known it.
	proof := fmt.Sprintf("/tau-proof-%d", time.Now().UnixNano())

	var last string
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, edge.URL+proof, nil)
		if err != nil {
			last = err.Error()
			return false
		}
		req.Host = at.host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err.Error()
			return false
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		last = fmt.Sprintf("%d: %s", res.StatusCode, body)
		if res.StatusCode != http.StatusOK {
			// THE GATEWAY SAYS A STATUS AND NOT WHY, on purpose -- a stranger is
			// not told what failed inside. The worker it asked will say, so a
			// red run names its cause instead of a timeout.
			_, werr := at.on(t.Context(), whoami, http.MethodGet, proof)
			last += fmt.Sprintf(" (the worker says: %v)", werr)
		}
		return res.StatusCode == http.StatusOK && strings.Contains(string(body), "GET "+proof+" ")
	}, 3*time.Minute, time.Second,
		// A COLD PULL AND A FIRST START are inside this budget; a daemon or firewall
		// that refuses says so in the last answer rather than as a timeout.
		"the application never answered: %s", &lastSaid{&last})
}
