//go:build dreaming

package service_test

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	httpClient "github.com/taubyte/tau/clients/http"
	patrickHttp "github.com/taubyte/tau/clients/http/patrick"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	servicesCommon "github.com/taubyte/tau/services/common"
	service "github.com/taubyte/tau/services/patrick"
	"gotest.tools/v3/assert"
)

// Needs a real GitHub token: Patrick's job routes check it with GitHub.
func TestDream_LiveLogs(t *testing.T) {
	token := os.Getenv("TAU_TEST_GITHUB_TOKEN")
	if token == "" {
		t.Skip("TAU_TEST_GITHUB_TOKEN not set")
	}

	service.GrantRepoAccess(token, commonTest.ConfigRepo.ID)

	http.DefaultClient = commonTest.CreateHttpClient()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	auth, err := simple.Auth()
	assert.NilError(t, err)

	patrickURL, err := u.GetURLHttp(u.Patrick().Node())
	assert.NilError(t, err)

	assert.NilError(t, commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo))
	servicesCommon.FakeSecret = true

	// a queued job: there is no monkey here, the test publishes its log
	assert.NilError(t, commonTest.PushJob(commonTest.ConfigPayload, patrickURL, commonTest.ConfigRepo))
	var jid string
	for deadline := time.Now().Add(5 * time.Second); jid == ""; time.Sleep(100 * time.Millisecond) {
		jobs, err := u.Patrick().KV().List(u.Context(), "/jobs/")
		assert.NilError(t, err)
		if len(jobs) > 0 {
			jid = strings.TrimPrefix(jobs[0], "/jobs/")
		}
		assert.Assert(t, time.Now().Before(deadline), "job not queued")
	}

	dial := func(jid string) *websocket.Conn {
		url := "ws" + strings.TrimPrefix(patrickURL, "http") + "/stream/logs/" + jid
		conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"github " + token}})
		if resp != nil && err != nil {
			t.Fatalf("dial: %s (%s)", err, resp.Status)
		}
		assert.NilError(t, err)
		return conn
	}

	node := simple.PeerNode()
	// the test publishes as the job's monkey
	assert.NilError(t, service.AssignJob(u.Context(), u.Patrick().KV(), jid, node.ID()))
	publish := func(off int64, data string, done bool) {
		b, err := cbor.Marshal(servicesCommon.LiveLogChunk{Job: jid, Offset: off, Data: []byte(data), Done: done})
		assert.NilError(t, err)
		assert.NilError(t, node.PubSubPublish(u.Context(), servicesCommon.LiveLogsTopic, b))
	}

	t.Run("StreamsWhileRunning", func(t *testing.T) {
		conn := dial(jid)
		defer conn.Close()

		msgs := make(chan string, 16)
		closed := make(chan error, 1)
		go func() {
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					closed <- err
					return
				}
				msgs <- string(data)
			}
		}()

		// the pubsub mesh between the two nodes takes a moment to form, so
		// the first chunk is repeated until it lands; Patrick drops repeats
		first := "cloning repo\n"
		start := time.Now()
		got := ""
	waitFirst:
		for deadline := time.Now().Add(30 * time.Second); ; {
			publish(0, first, false)
			select {
			case got = <-msgs:
				break waitFirst
			case <-time.After(500 * time.Millisecond):
			}
			assert.Assert(t, time.Now().Before(deadline), "first chunk never streamed")
		}
		assert.Equal(t, got, first)
		t.Logf("first chunk reached the client %s after the stream opened", time.Since(start).Round(time.Millisecond))

		// out of order: the later chunk waits until the gap is filled
		second, third := "building function ping\n", "done\n"
		publish(int64(len(first)+len(second)), third, false)
		publish(int64(len(first)), second, false)

		recv := func() string {
			select {
			case m := <-msgs:
				return m
			case err := <-closed:
				t.Fatalf("closed early: %s", err)
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for log")
			}
			return ""
		}
		rest := recv()
		for len(rest) < len(second+third) {
			rest += recv()
		}
		assert.Equal(t, rest, second+third)

		sent := time.Now()
		publish(int64(len(first+second+third)), "", true)
		select {
		case err := <-closed:
			var ce *websocket.CloseError
			assert.Assert(t, errors.As(err, &ce), "closed with %v", err)
			assert.Equal(t, ce.Code, websocket.CloseNormalClosure)
			t.Logf("stream closed %s after the job's last chunk", time.Since(sent).Round(time.Millisecond))
		case <-time.After(5 * time.Second):
			t.Fatal("stream did not close when the job ended")
		}
	})

	t.Run("LateClientGetsTheLogSoFar", func(t *testing.T) {
		conn := dial(jid)
		defer conn.Close()

		got := ""
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			got += string(data)
		}
		assert.Equal(t, got, "cloning repo\nbuilding function ping\ndone\n")
	})

	t.Run("CLIClient", func(t *testing.T) {
		c, err := patrickHttp.New(t.Context(), httpClient.URL(patrickURL), httpClient.Auth(token))
		assert.NilError(t, err)

		var out strings.Builder
		assert.NilError(t, c.StreamLogs(jid, &out))
		assert.Equal(t, out.String(), "cloning repo\nbuilding function ping\ndone\n")

		err = c.StreamLogs("QmNoSuchJob", &out)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("UnknownJob", func(t *testing.T) {
		conn := dial("QmNoSuchJob")
		defer conn.Close()

		_, _, err := conn.ReadMessage()
		var ce *websocket.CloseError
		assert.Assert(t, errors.As(err, &ce), "got %v", err)
		assert.Equal(t, ce.Code, websocket.CloseInternalServerErr)
		assert.Assert(t, strings.Contains(ce.Text, "not found"), ce.Text)
	})
}
