//go:build dreaming && docker_integration

package dream_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
)

// channel_application_test runs projects/with/channel/application: a channel
// served by a CONTAINER on one of its named ports. channel_test proves the half
// where a function answers, dispatched as a call; this is the half where the
// server is a process with a socket, and a channel is what the schema says it
// is — a byte channel. The dialer's bytes are written to the container's `http`
// port and what it writes back is the answer, with nothing wrapped around either.
//
// THE PAYLOAD IS A RAW HTTP/1.0 REQUEST because whoami is an HTTP server. Two
// things in the answer could only come from these bytes reaching its socket:
// the dialed path, which whoami echoes (re-serialized by Go, so as HTTP/1.1 and
// with Go's default User-Agent — whoami's rendering, not ours), and the status
// line's HTTP/1.0, which Go's server writes only to a 1.0 request. An
// invocation would have arrived as an HTTP/1.1 POST to "/".
//
// It runs under `make test-application`, for application_test's reason.
func TestAChannelServedByAContainerDeliversToItsNamedPort(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services:  []string{node.Register, node.Worker, node.Stats},
		Container: node.BackendDocker,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	require.NotEmpty(t, pids)

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	at := publish(t, pids[:1], deployed{project: "shop", name: "with/channel/application", host: "reserve.example.com"}, artifacts{"": cid})[0]

	// THE GRANT CARRIES THE PORT'S NAME, and is not refused for naming one.
	spec, err := at.node.Worker().Resolve(t.Context(), dialerTarget())
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 1)
	require.Empty(t, spec.Reaches[0].Refused)
	require.Equal(t, "http", spec.Reaches[0].Settings["serve/port"])

	proof := fmt.Sprintf("/tau-channel-%d", time.Now().UnixNano())
	request := "GET " + proof + " HTTP/1.0\r\nHost: whoami\r\n\r\n"

	var last string
	require.Eventually(t, func() bool {
		res, err := at.onCarrying(t.Context(), dialerTarget(), http.MethodPost, "/dial/reserve", request)
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
