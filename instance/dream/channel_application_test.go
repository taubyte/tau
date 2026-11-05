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
)

func TestAChannelServedByAContainerDeliversToItsNamedPort(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{instance.Register, instance.Worker, instance.Discovery, storageService.Descriptor.Name, instance.Builder},
		Container: containers.Docker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	require.NotEmpty(t, pids)

	at := publish(t, u, pids[:1], deployed{name: "with/channel/application", host: "reserve.example.com"}, artifacts{"": cacherWASM(t)})[0]

	// THE GRANT CARRIES THE PORT'S NAME, and is not refused for naming one.
	spec, err := at.node.Worker().Resolve(t.Context(), at.function("dialer"))
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 1)
	require.Empty(t, spec.Reaches[0].Refused)
	require.Equal(t, "http", spec.Reaches[0].Settings["serve/port"])

	proof := fmt.Sprintf("/tau-channel-%d", time.Now().UnixNano())
	request := "GET " + proof + " HTTP/1.0\r\nHost: whoami\r\n\r\n"

	var last string
	require.Eventually(t, func() bool {
		res, err := at.onCarrying(t.Context(), at.function("dialer"), http.MethodPost, "/dial/reserve", request)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 200 &&
			strings.HasPrefix(string(res.Body), "HTTP/1.0 200") &&
			strings.Contains(string(res.Body), "GET "+proof+" ")
	}, 3*time.Minute, time.Second, "the channel never reached the container's port: %s", &lastSaid{&last})
}
