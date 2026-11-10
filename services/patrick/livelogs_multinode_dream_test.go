//go:build dreaming

package service_test

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	servicesCommon "github.com/taubyte/tau/services/common"
	service "github.com/taubyte/tau/services/patrick"
	"gotest.tools/v3/assert"
)

// Two real Patricks: a log published once reaches both, a client can follow on
// either, and losing one leaves the other streaming.
func TestDream_LiveLogsAcrossPatricks(t *testing.T) {
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
			"patrick": {Others: map[string]int{"copies": 2}},
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

	patricks := u.AllPatrick()
	assert.Equal(t, len(patricks), 2)
	urls := make([]string, 2)
	for i, p := range patricks {
		urls[i], err = u.GetURLHttp(p.Node())
		assert.NilError(t, err)
	}

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	auth, err := simple.Auth()
	assert.NilError(t, err)
	assert.NilError(t, commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo))
	servicesCommon.FakeSecret = true

	assert.NilError(t, commonTest.PushJob(commonTest.ConfigPayload, urls[0], commonTest.ConfigRepo))
	var jid string
	for deadline := time.Now().Add(10 * time.Second); jid == ""; time.Sleep(100 * time.Millisecond) {
		jobs, err := patricks[0].KV().List(u.Context(), "/jobs/")
		assert.NilError(t, err)
		if len(jobs) > 0 {
			jid = strings.TrimPrefix(jobs[0], "/jobs/")
		}
		assert.Assert(t, time.Now().Before(deadline), "job not queued")
	}

	// the job's record reaches the second node through the shared database
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if _, err := patricks[1].KV().Get(u.Context(), "/jobs/"+jid); err == nil {
			break
		}
		assert.Assert(t, time.Now().Before(deadline), "job did not reach the second patrick")
	}

	type follower struct {
		text   chan string
		closed chan error
		conn   *websocket.Conn
	}
	follow := func(url string) *follower {
		ws := "ws" + strings.TrimPrefix(url, "http") + "/stream/logs/" + jid
		conn, resp, err := websocket.DefaultDialer.Dial(ws, http.Header{"Authorization": {"github " + token}})
		if resp != nil && err != nil {
			t.Fatalf("dial: %s (%s)", err, resp.Status)
		}
		assert.NilError(t, err)
		f := &follower{text: make(chan string, 64), closed: make(chan error, 1), conn: conn}
		go func() {
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					f.closed <- err
					return
				}
				f.text <- string(data)
			}
		}()
		return f
	}
	wait := func(f *follower, want string) {
		t.Helper()
		got := ""
		deadline := time.After(20 * time.Second)
		for !strings.Contains(got, want) {
			select {
			case s := <-f.text:
				got += s
			case err := <-f.closed:
				t.Fatalf("closed waiting for %q (got %q): %v", want, got, err)
			case <-deadline:
				t.Fatalf("timed out waiting for %q, got %q", want, got)
			}
		}
	}

	onFirst, onSecond := follow(urls[0]), follow(urls[1])
	defer onFirst.conn.Close()
	defer onSecond.conn.Close()
	time.Sleep(time.Second)

	node := simple.PeerNode()
	// the test publishes as the job's monkey
	assert.NilError(t, service.AssignJob(u.Context(), patricks[0].KV(), jid, node.ID()))
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if _, err := patricks[1].KV().Get(u.Context(), "/assigned/"+jid); err == nil {
			break
		}
		assert.Assert(t, time.Now().Before(deadline), "assignment did not reach the second patrick")
	}
	publish := func(run string, off int64, data string, done bool) {
		t.Helper()
		b, err := cbor.Marshal(servicesCommon.LiveLogChunk{Job: jid, Run: run, Offset: off, Data: []byte(data), Done: done})
		assert.NilError(t, err)
		assert.NilError(t, node.PubSubPublish(u.Context(), servicesCommon.LiveLogsTopic, b))
	}

	// a peer the job is not assigned to cannot write into its log
	forged, err := cbor.Marshal(servicesCommon.LiveLogChunk{Job: jid, Run: "first", Offset: 0, Data: []byte("FORGED")})
	assert.NilError(t, err)
	assert.NilError(t, patricks[1].Node().PubSubPublish(u.Context(), servicesCommon.LiveLogsTopic, forged))
	time.Sleep(time.Second)

	publish("first", 0, "from monkey one", false)
	wait(onFirst, "from monkey one")
	wait(onSecond, "from monkey one")
	for _, f := range []*follower{onFirst, onSecond} {
		select {
		case text := <-f.text:
			assert.Assert(t, !strings.Contains(text, "FORGED"), "a forged chunk reached a follower: %q", text)
		default:
		}
	}

	// a monkey dies, another runs the job again: both nodes follow the new run
	publish("second", 0, "from monkey two", false)
	wait(onFirst, "from monkey two")
	wait(onSecond, "from monkey two")

	// one Patrick goes away: its client is told, the other keeps streaming
	assert.NilError(t, u.KillNodeByNameID("patrick", patricks[0].Node().ID().String()))
	select {
	case <-onFirst.closed:
	case <-time.After(20 * time.Second):
		t.Error("the client of a dead patrick was not disconnected")
	}

	publish("second", int64(len("from monkey two")), " after the loss", false)
	wait(onSecond, "after the loss")

	publish("second", int64(len("from monkey two after the loss")), "", true)
	select {
	case err := <-onSecond.closed:
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Fatalf("stream ended with %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("stream did not end with the job")
	}
}
