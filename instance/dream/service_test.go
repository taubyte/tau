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

	"github.com/taubyte/tau/pkg/containers"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/specs"
)

func TestAServiceAnswersThroughTheGateway(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{instance.Register, instance.Worker, instance.Stats, instance.Gateway, storageService.Descriptor.Name, instance.Builder},
		Container: containers.Docker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	require.NotEmpty(t, pids)

	// NOTHING IS STAGED: the image is the artifact, named in the document.
	at := publish(t, u, pids[:1], deployed{name: "with/service", host: "whoami.example.com"}, artifacts{})[0]

	whoami := specs.Address{Project: at.project, Environment: "prod", Kind: "service", Name: "whoami"}

	// A REAL SOCKET IN FRONT OF THE GATEWAY, so the request is HTTP on the wire and
	// not a handler call.
	edge := httptest.NewServer(at.node.Handler())
	t.Cleanup(edge.Close)

	// A path only the container could answer with: whoami echoes the request line
	// into its body.
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
			// The gateway gives a status, not a cause; the worker says why, so a red run names
			// it.
			_, werr := at.on(t.Context(), whoami, http.MethodGet, proof)
			last += fmt.Sprintf(" (the worker says: %v)", werr)
		}
		return res.StatusCode == http.StatusOK && strings.Contains(string(body), "GET "+proof+" ")
	}, 3*time.Minute, time.Second,
		// A cold pull and first start fit this budget.
		"the service never answered: %s", &lastSaid{&last})
}
